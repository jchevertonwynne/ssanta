import * as openpgp from 'openpgp';

export interface TestKeyPair {
  privateKey: string;
  publicKey: string;
}

// Generates a passphrase-free ECC keypair suitable for E2E tests.
export async function generateTestKeyPair(name: string): Promise<TestKeyPair> {
  const { privateKey, publicKey } = await openpgp.generateKey({
    type: 'ecc',
    curve: 'curve25519Legacy',
    userIDs: [{ name, email: `${name}@e2e.test` }],
    passphrase: '',
    format: 'armored',
  });
  return { privateKey: privateKey as string, publicKey: publicKey as string };
}
