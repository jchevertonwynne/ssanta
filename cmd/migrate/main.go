package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/jchevertonwynne/ssanta/internal/db"
)

var errDatabaseURLRequired = errors.New("MIGRATE_DATABASE_URL or DATABASE_URL is required")

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
		return errDatabaseURLRequired
	}

	var err error
	if databaseSchema != "" {
		dbURL, err = applySchema(ctx, dbURL, databaseSchema)
		if err != nil {
			return err
		}
	}

	if err := db.Migrate(dbURL, migrationsDir); err != nil {
		return err
	}

	if adminUsername := strings.TrimSpace(os.Getenv("ADMIN_USERNAME")); adminUsername != "" {
		if err := db.SeedAdmin(ctx, dbURL, adminUsername); err != nil {
			return err
		}
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

func applySchema(ctx context.Context, dbURL, schema string) (string, error) {
	if err := db.CreateSchema(ctx, dbURL, schema); err != nil {
		return "", err
	}
	return db.WithSearchPath(dbURL, schema)
}

func envOrDefault(key, def string) string {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return def
	}
	return val
}
