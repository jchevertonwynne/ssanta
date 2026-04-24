package store

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jchevertonwynne/ssanta/internal/db"
)

var integrationPool *pgxpool.Pool                 //nolint:gochecknoglobals // test container pool
var integrationContainer testcontainers.Container //nolint:gochecknoglobals // test container
var integrationDSN string                         //nolint:gochecknoglobals // base DSN for per-test schemas

func TestMain(m *testing.M) {
	if os.Getenv("SSANTA_INTEGRATION") != "1" {
		os.Exit(m.Run())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	container, dsn, err := startPostgresContainer(ctx)
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "failed to start postgres container:", err)
		os.Exit(1)
	}
	integrationContainer = container
	integrationDSN = dsn

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "failed to connect to postgres:", err)
		_ = integrationContainer.Terminate(ctx)
		os.Exit(1)
	}
	integrationPool = pool

	// Install database-level extensions once before parallel tests run.
	// CREATE EXTENSION is not concurrency-safe so parallel migrations would race.
	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS citext"); err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "failed to create citext extension:", err)
		_ = integrationContainer.Terminate(ctx)
		os.Exit(1)
	}

	code := m.Run()

	cancel()
	integrationPool.Close()
	_ = integrationContainer.Terminate(ctx)
	os.Exit(code)
}

func requireIntegration(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SSANTA_INTEGRATION") != "1" {
		t.Skip("set SSANTA_INTEGRATION=1 to run integration tests")
	}
	if integrationPool == nil {
		t.Fatalf("integration pool not initialized")
	}

	schema := testSchemaName(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	if err := db.CreateSchema(ctx, integrationDSN, schema); err != nil {
		t.Fatalf("create test schema %q: %v", schema, err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanCancel()
		_, _ = integrationPool.Exec(cleanCtx, `DROP SCHEMA "`+schema+`" CASCADE`)
	})

	// Include public in the search path so extension types (e.g. citext) installed
	// in public are visible, while unqualified table creates still land in schema.
	schemaDSN, err := db.WithSearchPath(integrationDSN, schema+",public")
	if err != nil {
		t.Fatalf("build schema DSN: %v", err)
	}

	migsDir, err := migrationsDir()
	if err != nil {
		t.Fatalf("locate migrations dir: %v", err)
	}
	if err := db.Migrate(schemaDSN, migsDir); err != nil {
		t.Fatalf("migrate test schema %q: %v", schema, err)
	}

	pool, err := db.Connect(ctx, schemaDSN)
	if err != nil {
		t.Fatalf("connect to test schema %q: %v", schema, err)
	}
	t.Cleanup(pool.Close)

	return pool
}

func testSchemaName(t *testing.T) string {
	t.Helper()
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '_'
		}
	}, t.Name())
	const maxlength = 57 // keep total ≤ 63 bytes (PostgreSQL identifier limit)
	if len(name) > maxlength {
		name = name[:maxlength]
	}
	return "test_" + name
}

func migrationsDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// When running tests for package internal/store, wd is typically .../internal/store.
	dir := filepath.Clean(filepath.Join(wd, "..", "..", "migrations"))
	return filepath.Abs(dir)
}

func startPostgresContainer(ctx context.Context) (testcontainers.Container, string, error) {
	const (
		pgUser = "ssanta"
		pgPass = "ssanta"
		pgDB   = "ssanta"
	)

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     pgUser,
			"POSTGRES_PASSWORD": pgPass,
			"POSTGRES_DB":       pgDB,
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", err
	}

	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, "", err
	}
	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, "", err
	}

	dsn := fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", pgUser, pgPass, net.JoinHostPort(host, port.Port()), pgDB)
	return container, dsn, nil
}
