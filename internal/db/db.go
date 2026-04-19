package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()

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

func Migrate(url, dir string) error {
	m, err := migrate.New("file://"+dir, url)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	slog.Info("migrations applied")
	return nil
}

func GrantRuntimePrivileges(ctx context.Context, adminDatabaseURL, schema, role string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		schema = "public"
	}
	role = strings.TrimSpace(role)
	if role == "" {
		return errors.New("runtime role is required")
	}

	pool, err := Connect(ctx, adminDatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Ensure the role exists.
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1 FROM pg_roles WHERE rolname = $1", role).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("runtime role %q does not exist", role)
		}
		return fmt.Errorf("check runtime role exists: %w", err)
	}

	// Default privileges must be set by the role that will create future objects.
	var currentUser string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil {
		return fmt.Errorf("get current_user: %w", err)
	}

	schemaIdent := quoteIdent(schema)
	roleIdent := quoteIdent(role)
	creatorIdent := quoteIdent(currentUser)

	stmts := []string{
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
