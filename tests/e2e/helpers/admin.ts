import { execFileSync } from 'child_process';

const DSN = 'postgres://ssanta:ssanta@localhost:5432/ssanta_e2e?sslmode=disable';

export function promoteToAdmin(username: string): void {
  const escaped = username.replace(/'/g, "''");
  const sql = `INSERT INTO admins (user_id) SELECT id FROM users WHERE username = '${escaped}' ON CONFLICT DO NOTHING;`;
  console.log('promoteToAdmin SQL:', sql);
  const out = execFileSync('psql', [DSN, '-c', sql, '-t']);
  const trimmed = out.toString().trim();
  console.log('promoteToAdmin result:', trimmed);
  if (trimmed !== 'INSERT 0 1') {
    throw new Error(`Failed to promote ${username} to admin: ${trimmed}`);
  }
}
