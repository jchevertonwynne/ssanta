import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';
import { generateTestKeyPair } from '../helpers/pgp';
import * as path from 'path';

async function routeOpenPGP(page: import('@playwright/test').Page) {
  const localOpenpgp = path.resolve(__dirname, '..', 'node_modules', 'openpgp', 'dist', 'openpgp.min.js');
  await page.route('**cdn.jsdelivr.net**openpgp**', (route) =>
    route.fulfill({ path: localOpenpgp })
  );
}

test.describe('room persistence', () => {
  test('PGP key auto-loads after page reload when persist toggle is on', async ({
    authedPage: { page, username },
  }) => {
    await routeOpenPGP(page);
    const { privateKey } = await generateTestKeyPair(username);

    const roomId = await createRoom(page, `persist-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    // Open PGP section and enable persistence before loading the key
    const pgpSectionOpen = await room.pgpSection.evaluate((el) => (el as HTMLDetailsElement).open);
    if (!pgpSectionOpen) {
      await room.pgpSection.locator('summary').first().click();
    }
    await room.pgpPersistToggle.check();
    // The load handler also triggers autoHandlePGPVerification() which POSTs the
    // public key to /pgp-key and swaps the sidebar. Wait for that POST so the
    // page is stable before reload — otherwise localStorage may not be flushed.
    const pgpUploadDone = page.waitForResponse(
      (r) => r.url().includes('/pgp-key') && r.request().method() === 'POST'
    );
    await room.loadPGPKey(privateKey);
    await pgpUploadDone;
    await expect(room.pgpKeyStatus).toContainText('Key loaded. Fingerprint:');

    // Reload the page — the auto-load IIFE should read from localStorage and restore the key
    await routeOpenPGP(page);
    await page.reload();
    const room2 = await navigateToRoom(page, roomId);
    await expect(room2.pgpKeyStatus).toContainText('Key loaded. Fingerprint:', { timeout: 10_000 });
  });

  test('PGP key is not available in a new browser session when persist is off', async ({
    authedPage: { page, username },
    browser,
  }) => {
    await routeOpenPGP(page);
    const { privateKey } = await generateTestKeyPair(username);

    const roomId = await createRoom(page, `no-persist-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    // Ensure persist toggle is off (key goes to sessionStorage)
    const pgpSectionOpen = await room.pgpSection.evaluate((el) => (el as HTMLDetailsElement).open);
    if (!pgpSectionOpen) {
      await room.pgpSection.locator('summary').first().click();
    }
    await room.pgpPersistToggle.uncheck();
    await room.loadPGPKey(privateKey);
    await expect(room.pgpKeyStatus).toContainText('Key loaded. Fingerprint:');

    // Open the same room in a brand-new browser context (new session → fresh sessionStorage)
    const newCtx = await browser.newContext();
    const newPage = await newCtx.newPage();
    await routeOpenPGP(newPage);

    // Log in as the same user in the new context
    await newPage.goto('/');
    await newPage.fill('input[name=username]', username);
    await newPage.fill('input[name=password]', 'TestPassword123!');
    await newPage.click('#login-btn');
    await expect(newPage.locator('.bar')).toContainText(`Logged in as ${username}`, { timeout: 10_000 });

    const room2 = await navigateToRoom(newPage, roomId);
    // Key should NOT be auto-loaded in the new session (sessionStorage is context-scoped)
    await expect(room2.pgpKeyStatus).toHaveText('', { timeout: 5_000 });

    await newCtx.close();
  });
});
