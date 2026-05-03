import { execFileSync } from 'child_process';
import * as path from 'path';

const repoRoot = path.resolve(__dirname, '..', '..');

export default async function globalTeardown() {
  if (process.env.CI) {
    // In CI, tear down the DB container and its volume to leave a clean environment.
    execFileSync('docker', ['compose', 'down', '-v'], {
      cwd: repoRoot,
      stdio: 'inherit',
    });
  }
  // Locally, leave the DB container running so subsequent runs skip the startup wait.
}
