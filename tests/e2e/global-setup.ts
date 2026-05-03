import { execFileSync } from 'child_process';

const E2E_DSN = 'postgres://ssanta:ssanta@localhost:5432/ssanta_e2e?sslmode=disable';

export default async function globalSetup() {
  // Truncate all application tables to give each e2e run a clean slate
  execFileSync('psql', [E2E_DSN, '-c', `
    DO $$ DECLARE
      r RECORD;
    BEGIN
      FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename != 'schema_migrations') LOOP
        EXECUTE 'TRUNCATE TABLE ' || quote_ident(r.tablename) || ' CASCADE';
      END LOOP;
    END $$;
  `]);

  console.log('E2E setup complete: e2e tables truncated.');
}
