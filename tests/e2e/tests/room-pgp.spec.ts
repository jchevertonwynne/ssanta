import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom, RoomDetailPage } from '../pages/RoomDetailPage';
import { generateTestKeyPair } from '../helpers/pgp';
import * as path from 'path';

// Route CDN requests for openpgp.js to the local node_modules copy so tests
// work in network-restricted CI environments.
async function routeOpenPGP(page: import('@playwright/test').Page) {
  const localOpenpgp = path.resolve(__dirname, '..', 'node_modules', 'openpgp', 'dist', 'openpgp.min.js');
  await page.route('**cdn.jsdelivr.net**openpgp**', (route) =>
    route.fulfill({ path: localOpenpgp })
  );
}

test.describe('room PGP', () => {
  test('load private key updates status to show fingerprint', async ({ authedPage: { page } }) => {
    await routeOpenPGP(page);
    const { privateKey } = await generateTestKeyPair('tester');
    const roomId = await createRoom(page, `pgp-load-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();
    await room.loadPGPKey(privateKey);
    await expect(room.pgpKeyStatus).toContainText('Key loaded. Fingerprint:');
  });

  test('PGP-encrypted message shows Decrypted badge for key holder', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    await routeOpenPGP(pageA);
    await routeOpenPGP(pageB);

    const keyA = await generateTestKeyPair(usernameA);
    const keyB = await generateTestKeyPair(usernameB);

    // User A creates a room and enables PGP required
    const roomId = await createRoom(pageA, `pgp-enc-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();
    await roomA.enablePGPRequired();

    // User A loads their key (triggers verification flow)
    await roomA.loadPGPKey(keyA.privateKey);

    // Make room public so User B can join
    const publicCheckbox = pageA.locator('label:has-text("Public room") input[type=checkbox]');
    if (!(await publicCheckbox.isChecked())) {
      await publicCheckbox.click();
      await pageA.waitForResponse((r) => r.url().includes('/public'));
    }

    // User B navigates to the room, joins, and loads their key
    await pageB.goto('/');
    const roomB = await navigateToRoom(pageB, roomId);
    await roomB.joinRoom();
    await roomB.loadPGPKey(keyB.privateKey);

    // User B's key is now verified; User A reloads the sidebar to pick up User B's public key
    // (the member list update comes via WebSocket)
    await pageA.waitForTimeout(1_000);

    // User A sends a message — should be encrypted with User B's key
    await roomA.sendMessage('secret message');

    // User A sees a "Decrypted" badge on their own sent message (self-encrypted)
    await expect(roomA.chatMessages).toContainText('Decrypted', { timeout: 10_000 });

    // User B should also see the message decrypted
    await expect(roomB.chatMessages).toContainText('Decrypted', { timeout: 10_000 });
  });

  test('clear key removes stored key and hides status', async ({ authedPage: { page } }) => {
    await routeOpenPGP(page);
    const { privateKey } = await generateTestKeyPair('cleartest');
    const roomId = await createRoom(page, `pgp-clear-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();
    await room.loadPGPKey(privateKey);
    await expect(room.pgpKeyStatus).toContainText('Key loaded. Fingerprint:');

    await page.locator('#pgp-clear-key-btn').click();
    await expect(room.pgpKeyStatus).toHaveText('');
    await expect(room.pgpPrivateKeyInput).toHaveValue('');
  });
});
