import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';

test.describe('invite lifecycle', () => {
  test('creator invites user who accepts and can chat', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    const roomId = await createRoom(pageA, `invite-accept-${Date.now()}`);
    await navigateToRoom(pageA, roomId);

    await expect(pageA.locator('select[name=invitee_username]')).toBeVisible({ timeout: 5_000 });
    await pageA.locator('select[name=invitee_username]').selectOption({ value: usernameB });
    await pageA.locator('button:has-text("Send invite")').click();
    await pageA.waitForResponse((r) => r.url().includes('/invites'));

    // User B loads home — invite appears server-rendered in #content-invites
    await pageB.goto('/');
    await pageB.waitForLoadState('networkidle');
    await expect(pageB.locator('#content-invites')).toContainText(usernameA, { timeout: 5_000 });

    // Accept → HTMX swaps #content with the room detail
    await pageB.locator('#content-invites button:has-text("Accept")').first().click();
    // Wait for the room detail to load (HTMX swapped #content)
    await pageB.waitForSelector('#chat-messages', { timeout: 10_000 });

    // Navigate directly to the room to avoid a WS race where rooms_updated
    // reloads the home page content before we can interact with the room.
    await pageB.goto(`/rooms/room_id:${roomId}`);
    await pageB.waitForSelector('#chat-messages', { timeout: 10_000 });

    // User B is now a member and can send a message
    await expect(pageB.locator('#chat-input')).toBeEnabled({ timeout: 5_000 });
    await pageB.locator('#chat-input').fill('accepted!');
    await pageB.locator('#chat-form button[type=submit]').click();
    await expect(pageB.locator('#chat-messages')).toContainText('accepted!', { timeout: 5_000 });
  });

  test('creator can cancel a pending invite', async ({
    authedPage: { page: pageA },
    secondAuthedPage: { username: usernameB },
  }) => {
    const roomId = await createRoom(pageA, `invite-cancel-${Date.now()}`);
    await navigateToRoom(pageA, roomId);

    await expect(pageA.locator('select[name=invitee_username]')).toBeVisible({ timeout: 5_000 });
    await pageA.locator('select[name=invitee_username]').selectOption({ value: usernameB });
    await pageA.locator('button:has-text("Send invite")').click();
    await pageA.waitForResponse((r) => r.url().includes('/invites'));

    // Pending invite appears in #room-dynamic
    await expect(pageA.locator('#room-dynamic')).toContainText(usernameB, { timeout: 5_000 });

    // Cancel via hx-confirm dialog
    pageA.once('dialog', (d) => d.accept());
    await pageA.locator('#room-dynamic button:has-text("Cancel")').click();
    await expect(pageA.locator('#room-dynamic')).not.toContainText(usernameB, { timeout: 5_000 });
  });

  test('user can decline an invite', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    const roomId = await createRoom(pageA, `invite-decline-${Date.now()}`);
    await navigateToRoom(pageA, roomId);

    await expect(pageA.locator('select[name=invitee_username]')).toBeVisible({ timeout: 5_000 });
    await pageA.locator('select[name=invitee_username]').selectOption({ value: usernameB });
    await pageA.locator('button:has-text("Send invite")').click();
    await pageA.waitForResponse((r) => r.url().includes('/invites'));

    await pageB.goto('/');
    await pageB.waitForLoadState('networkidle');
    await expect(pageB.locator('#content-invites')).toContainText(usernameA, { timeout: 5_000 });

    await pageB.locator('#content-invites button:has-text("Refuse")').first().click();
    await expect(pageB.locator('#content-invites')).toContainText('None yet.', { timeout: 5_000 });
  });
});
