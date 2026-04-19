package main
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jchevertonwynne/ssanta/internal/db"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	migrationsDir := envOrDefault("MIGRATIONS_DIR", "migrations")
	databaseSchema := strings.TrimSpace(os.Getenv("DATABASE_SCHEMA"))

	dbURL := strings.TrimSpace(os.Getenv("MIGRATE_DATABASE_URL"))
	if dbURL == "" {
		dbURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if dbURL == "" {
		return fmt.Errorf("MIGRATE_DATABASE_URL or DATABASE_URL is required")
	}

	if databaseSchema != "" {
		if err := db.CreateSchema(ctx, dbURL, databaseSchema); err != nil {
			return err
		}
		urlWithSP, err := db.WithSearchPath(dbURL, databaseSchema)
		if err != nil {
			return err
		}
		dbURL = urlWithSP
	}

	return db.Migrate(dbURL, migrationsDir)
}

func envOrDefault(key, def string) string {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return def
	}
	return val
}
