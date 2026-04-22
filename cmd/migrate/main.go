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
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
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
	runtimeDBUser := strings.TrimSpace(os.Getenv("RUNTIME_DB_USER"))

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

	if err := db.Migrate(dbURL, migrationsDir); err != nil {
		return err
	}

	if runtimeDBUser != "" {
		runtimeDBPass := strings.TrimSpace(os.Getenv("RUNTIME_DB_PASS"))
		if err := db.GrantRuntimePrivileges(ctx, dbURL, databaseSchema, runtimeDBUser, runtimeDBPass); err != nil {
			return err
		}
		slog.Info("runtime privileges granted", "role", runtimeDBUser, "schema", envOrDefault("DATABASE_SCHEMA", "public")) //nolint:gosec
	}

	return nil
}

func envOrDefault(key, def string) string {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return def
	}
	return val
}
