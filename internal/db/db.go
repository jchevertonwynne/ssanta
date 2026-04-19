package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
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

	pool, err := Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	query := "CREATE SCHEMA IF NOT EXISTS " + quoteIdent(schema)
	if _, err := pool.Exec(ctx, query); err != nil {
		return fmt.Errorf("create schema %q: %w", schema, err)
	}
	return nil
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
