package config

import (
	"testing"
	"time"
)

func TestLoad_RequiredMissing(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SESSION_SECRET", "")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("SESSION_SECRET", "this-is-a-32-byte-long-test-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr: got %q", cfg.HTTPAddr)
	}
	if cfg.MigrationsDir != "migrations" {
		t.Fatalf("MigrationsDir: got %q", cfg.MigrationsDir)
	}
	if cfg.DatabaseSchema != "" {
		t.Fatalf("DatabaseSchema: got %q", cfg.DatabaseSchema)
	}
	if cfg.MigrateDatabaseURL != "" {
		t.Fatalf("MigrateDatabaseURL: got %q", cfg.MigrateDatabaseURL)
	}
	if cfg.InviteMaxAge != 24*time.Hour {
		t.Fatalf("InviteMaxAge: got %v", cfg.InviteMaxAge)
	}
	if cfg.JanitorInterval != 5*time.Minute {
		t.Fatalf("JanitorInterval: got %v", cfg.JanitorInterval)
	}
}

func TestLoad_ParsesOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("SESSION_SECRET", "this-is-a-32-byte-long-test-secret")
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("DATABASE_SCHEMA", "ssanta")
	t.Setenv("MIGRATE_DATABASE_URL", "postgres://migrate:p@localhost:5432/db?sslmode=disable")
	t.Setenv("INVITE_MAX_AGE", "2h")
	t.Setenv("JANITOR_INTERVAL", "5s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":9999" {
		t.Fatalf("HTTPAddr: got %q", cfg.HTTPAddr)
	}
	if cfg.DatabaseSchema != "ssanta" {
		t.Fatalf("DatabaseSchema: got %q", cfg.DatabaseSchema)
	}
	if cfg.MigrateDatabaseURL != "postgres://migrate:p@localhost:5432/db?sslmode=disable" { //nolint:gosec
		t.Fatalf("MigrateDatabaseURL: got %q", cfg.MigrateDatabaseURL)
	}
	if cfg.InviteMaxAge != 2*time.Hour {
		t.Fatalf("InviteMaxAge: got %v", cfg.InviteMaxAge)
	}
	if cfg.JanitorInterval != 5*time.Second {
		t.Fatalf("JanitorInterval: got %v", cfg.JanitorInterval)
	}
}

func TestLoad_ShortSessionSecretRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("SESSION_SECRET", "too-short")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for short SESSION_SECRET")
	}
}

func TestLoad_InvalidDurationErrors(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("SESSION_SECRET", "this-is-a-32-byte-long-test-secret")
	t.Setenv("INVITE_MAX_AGE", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error")
	}
}
