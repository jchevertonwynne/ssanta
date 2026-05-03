import { defineConfig, devices } from '@playwright/test';
import { execFileSync } from 'child_process';
import * as path from 'path';

const repoRoot = path.resolve(__dirname, '..', '..');
const ADMIN_DSN = 'postgres://ssanta:ssanta@localhost:5432/postgres?sslmode=disable';
const E2E_DSN = 'postgres://ssanta:ssanta@localhost:5432/ssanta_e2e?sslmode=disable';
const BASE_URL = 'http://127.0.0.1:8099';

// Ensure the Docker DB is running before Playwright starts the webServer.
execFileSync('docker', ['compose', 'up', '-d', '--wait', 'db'], {
  cwd: repoRoot,
  stdio: 'inherit',
});

const pgEnv = { ...process.env, PGPASSWORD: 'ssanta' };

// Wait until Postgres is actually accepting connections
for (let i = 0; i < 30; i++) {
  try {
    execFileSync('pg_isready', ['-h', 'localhost', '-p', '5432', '-U', 'ssanta'], { env: pgEnv, stdio: 'ignore' });
    break;
  } catch {
    execFileSync('sleep', ['1']);
  }
}

// Create the e2e-exclusive database if it doesn't exist yet.
try {
  execFileSync('createdb', ['-h', 'localhost', '-p', '5432', '-U', 'ssanta', 'ssanta_e2e'], { env: pgEnv, stdio: 'ignore' });
} catch {
  // already exists
}

// Run migrations against the e2e database.
execFileSync('go', ['run', './cmd/migrate'], {
  cwd: repoRoot,
  env: {
    ...process.env,
    DATABASE_URL: E2E_DSN,
    MIGRATIONS_DIR: 'migrations',
  },
  stdio: 'inherit',
});

// Build the server binary that webServer will run.
execFileSync('go', ['build', '-o', 'server', './cmd/server'], {
  cwd: repoRoot,
  stdio: 'inherit',
});

export default defineConfig({
  testDir: './tests',
  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
  },
  webServer: {
    command: path.join(repoRoot, 'server'),
    url: `${BASE_URL}/healthz`,
    reuseExistingServer: false,
    timeout: 20_000,
    env: {
      DATABASE_URL: E2E_DSN,
      SESSION_SECRET: 'e2e-test-secret-fixed-32-bytes!!',
      SECURE_COOKIES: 'false',
      HTTP_ADDR: ':8099',
      ARGON2_MEMORY: '8192',
      ARGON2_ITERATIONS: '1',
      ARGON2_PARALLELISM: '1',
      RATE_LIMIT_AUTH_MAX: '1000',
      RATE_LIMIT_AUTH_WINDOW: '1m',
      RATE_LIMIT_ROOM_MAX: '1000',
      RATE_LIMIT_ROOM_WINDOW: '1m',
      RATE_LIMIT_WS_MAX: '1000',
      RATE_LIMIT_WS_WINDOW: '1m',
      RATE_LIMIT_SEARCH_MAX: '1000',
      RATE_LIMIT_SEARCH_WINDOW: '1m',
      RATE_LIMIT_INVITE_MAX: '1000',
      RATE_LIMIT_INVITE_WINDOW: '1m',
      RATE_LIMIT_DM_MAX: '1000',
      RATE_LIMIT_DM_WINDOW: '1m',
      WS_MSG_BURST: '1000',
      WS_MSG_REFILL_PER_SEC: '1000',
    },
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
});
