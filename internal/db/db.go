// Package db provides PostgreSQL connection, migration, and privilege helpers.
package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errRuntimeRoleRequired = errors.New("runtime role is required")

// WithSearchPath adds a PostgreSQL search_path query parameter when schema is set.
func WithSearchPath(databaseURL, schema string) (string, error) {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return databaseURL, nil
	}

	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse db url: %w", err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// CreateSchema creates the configured schema when needed.
func CreateSchema(ctx context.Context, databaseURL, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return nil
	}
	if schema == "public" {
		return nil
	}

	pool, err := Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	query := "CREATE SCHEMA IF NOT EXISTS " + quoteIdent(schema)
	if _, err := pool.Exec(ctx, query); err != nil {
		exists, existsErr := schemaExists(ctx, pool, schema)
		if existsErr == nil && exists {
			return nil
		}
		return fmt.Errorf("create schema %q: %w (hint: pre-create the schema or set MIGRATE_DATABASE_URL with a role that has CREATE privilege on the database)", schema, err)
	}
	return nil
}

func schemaExists(ctx context.Context, pool *pgxpool.Pool, schema string) (bool, error) {
	var one int
	err := pool.QueryRow(ctx, "SELECT 1 FROM pg_namespace WHERE nspname = $1", schema).Scan(&one)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

type queryNameKey struct{}

// WithQueryName annotates ctx with a short name used as the OTel span name for
// the next database query executed with that context.
func WithQueryName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, queryNameKey{}, name)
}

var reWhitespace = regexp.MustCompile(`\s+`)

// sqlSlug converts a raw SQL statement into a short "verb.table" slug for span names.
func sqlSlug(stmt string) string {
	norm := strings.ToUpper(reWhitespace.ReplaceAllString(strings.TrimSpace(stmt), " "))

	verb, rest, _ := strings.Cut(norm, " ")

	// For CTEs, skip past the WITH clause to find the real verb and table.
	if verb == "WITH" {
		if _, after, ok := strings.Cut(rest, ") "); ok {
			inner := strings.TrimSpace(after)
			verb, rest, _ = strings.Cut(inner, " ")
		}
	}

	lv := strings.ToLower(verb)
	switch verb {
	case "SELECT":
		return lv + "." + tableAfterKeyword(rest, "FROM")
	case "INSERT":
		return lv + "." + tableAfterKeyword(rest, "INTO")
	case "UPDATE":
		return lv + "." + firstWord(rest)
	case "DELETE":
		return lv + "." + tableAfterKeyword(rest, "FROM")
	default:
		if verb == "" {
			return "unknown"
		}
		return lv
	}
}

// tableAfterKeyword finds the first word after keyword in s, strips any schema
// prefix (e.g. "public.users" → "users") and returns it lowercased.
func tableAfterKeyword(s, keyword string) string {
	kw := keyword + " "
	_, after, ok := strings.Cut(s, kw)
	if !ok {
		return strings.ToLower(strings.TrimSpace(strings.Fields(s)[0]))
	}
	return firstWord(after)
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	end := strings.IndexAny(s, " \t\n(,;")
	if end < 0 {
		end = len(s)
	}
	word := s[:end]
	// Strip schema prefix
	if dot := strings.LastIndex(word, "."); dot >= 0 {
		word = word[dot+1:]
	}
	return strings.ToLower(word)
}

// Connect opens a PostgreSQL connection pool with observability enabled.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}

	// pgxpool.ParseConfig doesn't always propagate runtime params like search_path
	// from URL query parameters, so explicitly apply it when present.
	if sp, ok := searchPathFromURL(url); ok {
		if cfg.ConnConfig.RuntimeParams == nil {
			cfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		cfg.ConnConfig.RuntimeParams["search_path"] = sp
	}
	cfg.MaxConnLifetime = 30 * time.Minute

	// Configure OpenTelemetry tracer for pgx
	cfg.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithSpanNameCtxFunc(func(ctx context.Context, stmt string) string {
		if name, ok := ctx.Value(queryNameKey{}).(string); ok && name != "" {
			return name
		}
		return sqlSlug(stmt)
	}))

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return pool, nil
}

func searchPathFromURL(databaseURL string) (string, bool) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", false
	}
	sp := strings.TrimSpace(u.Query().Get("search_path"))
	if sp == "" {
		return "", false
	}
	return sp, true
}

// Migrate runs database migrations from dir against url.
func Migrate(url, dir string) error {
	m, err := migrate.New("file://"+dir, url)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer m.Close() //nolint:errcheck

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	slog.Info("migrations applied")
	return nil
}

// SeedAdmin promotes a named user to admin if they exist. Safe to call
// multiple times (ON CONFLICT DO NOTHING). If the user does not exist yet,
// a warning is logged and nil is returned — re-run migrate after the user registers.
func SeedAdmin(ctx context.Context, databaseURL, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil
	}

	pool, err := Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	tag, err := pool.Exec(ctx,
		`INSERT INTO admins (user_id, granted_by)
		 SELECT id, NULL FROM users WHERE LOWER(username) = LOWER($1)
		 ON CONFLICT DO NOTHING`,
		username,
	)
	if err != nil {
		return fmt.Errorf("seed admin %q: %w", username, err)
	}
	if tag.RowsAffected() == 0 {
		slog.Warn("admin seed: user not found (register the account then re-run migrate)", "username", username)
	} else {
		slog.Info("admin seeded", "username", username)
	}
	return nil
}

// GrantRuntimePrivileges grants runtime access to the application role.
//
//nolint:cyclop,funlen
func GrantRuntimePrivileges(ctx context.Context, adminDatabaseURL, schema, role, password string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		schema = "public"
	}
	role = strings.TrimSpace(role)
	if role == "" {
		return errRuntimeRoleRequired
	}

	pool, err := Connect(ctx, adminDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	roleIdent := quoteIdent(role)

	// Create the role if it doesn't exist, otherwise rotate its password.
	var one int
	err = pool.QueryRow(ctx, "SELECT 1 FROM pg_roles WHERE rolname = $1", role).Scan(&one)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := pool.Exec(ctx, "CREATE ROLE "+roleIdent+" WITH LOGIN PASSWORD "+quoteLiteral(password)); err != nil {
			return fmt.Errorf("create role %q: %w", role, err)
		}
	case err != nil:
		return fmt.Errorf("check runtime role exists: %w", err)
	case password != "":
		if _, err := pool.Exec(ctx, "ALTER ROLE "+roleIdent+" WITH PASSWORD "+quoteLiteral(password)); err != nil {
			return fmt.Errorf("rotate password for role %q: %w", role, err)
		}
	}

	var dbName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		return fmt.Errorf("get current database: %w", err)
	}

	// Default privileges must be set by the role that will create future objects.
	var currentUser string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil {
		return fmt.Errorf("get current_user: %w", err)
	}

	schemaIdent := quoteIdent(schema)
	creatorIdent := quoteIdent(currentUser)

	stmts := []string{
		"GRANT CONNECT ON DATABASE " + quoteIdent(dbName) + " TO " + roleIdent,
		"GRANT USAGE ON SCHEMA " + schemaIdent + " TO " + roleIdent,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + schemaIdent + " TO " + roleIdent,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " + schemaIdent + " TO " + roleIdent,
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + creatorIdent + " IN SCHEMA " + schemaIdent + " GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + roleIdent,
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + creatorIdent + " IN SCHEMA " + schemaIdent + " GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO " + roleIdent,
		"REVOKE CREATE ON SCHEMA " + schemaIdent + " FROM " + roleIdent,
	}

	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("grant privileges: %w", err)
		}
	}

	return nil
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
