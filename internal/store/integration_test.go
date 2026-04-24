package store

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jchevertonwynne/ssanta/internal/db"
)

var integrationPool *pgxpool.Pool                 //nolint:gochecknoglobals // test container pool
var integrationContainer testcontainers.Container //nolint:gochecknoglobals // test container

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

	migrationsDir, err := migrationsDir()
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "failed to locate migrations dir:", err)
		_ = integrationContainer.Terminate(ctx)
		os.Exit(1)
	}

	if err := db.Migrate(dsn, migrationsDir); err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "failed to run migrations:", err)
		_ = integrationContainer.Terminate(ctx)
		os.Exit(1)
	}

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "failed to connect to postgres:", err)
		_ = integrationContainer.Terminate(ctx)
		os.Exit(1)
	}
	integrationPool = pool

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
	resetDB(t, integrationPool)
	return integrationPool
}

func resetDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `TRUNCATE room_invites, room_users, rooms, users, messages RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset db: %v", err)
	}
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
