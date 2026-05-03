import { test, expect } from '../fixtures/auth';
import { RoomDetailPage } from '../pages/RoomDetailPage';

test.describe('direct messages', () => {
  test('starting a DM navigates to a DM room without whisper target', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    // User A selects User B and starts a DM
    await pageA.goto('/');
    await pageA.waitForSelector('#dm-partner-select');
    await pageA.locator('#dm-partner-select').selectOption({ label: usernameB });
    await pageA.locator('[data-action="start-dm"]').click();

    // The fetch() in startDM() does window.location.href = r.url on redirect
    await pageA.waitForURL(/\/rooms\//, { timeout: 10_000 });

    // Extract the DM room ID from User A's current URL
    const roomUrl = pageA.url();
    const roomIdMatch = roomUrl.match(/\/rooms\/(?:room_id:)?(\d+)/);
    if (!roomIdMatch) throw new Error('Could not extract DM room ID: ' + roomUrl);
    const dmRoomId = parseInt(roomIdMatch[1], 10);

    // DM rooms have no #chat-target select
    await expect(pageA.locator('#chat-target')).toHaveCount(0);
    // DM room is usable — chat input is enabled
    await expect(pageA.locator('#chat-input')).toBeEnabled({ timeout: 5_000 });

    // User A sends a message
    const dmText = `dm-msg-${Date.now()}`;
    await pageA.locator('#chat-input').fill(dmText);
    await pageA.locator('#chat-form button[type=submit]').click();
    await expect(pageA.locator('#chat-messages')).toContainText(dmText, { timeout: 5_000 });

    // User B navigates directly to the DM room (avoids content-WS race from the home page)
    await pageB.goto(`/rooms/room_id:${dmRoomId}`);
    await pageB.waitForSelector('#chat-messages', { timeout: 10_000 });

    // User B sees User A's message
    await expect(pageB.locator('#chat-messages')).toContainText(dmText, { timeout: 5_000 });

    // DM room also has no #chat-target for User B
    await expect(pageB.locator('#chat-target')).toHaveCount(0);
  });

  test('DM room shows partner name as heading', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    await pageA.goto('/');
    await pageA.waitForSelector('#dm-partner-select');
    await pageA.locator('#dm-partner-select').selectOption({ label: usernameB });
    await pageA.locator('[data-action="start-dm"]').click();
    await pageA.waitForURL(/\/rooms\//, { timeout: 10_000 });

    // The sidebar heading should show the partner's username, not a room display name
    await expect(pageA.locator('#room-sidebar h2')).toContainText(usernameB, { timeout: 5_000 });
  });
});
