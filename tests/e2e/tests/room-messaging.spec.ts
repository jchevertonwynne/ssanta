import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom, RoomDetailPage } from '../pages/RoomDetailPage';

test.describe('room messaging', () => {
  test('sent message appears in chat', async ({ authedPage: { page, username } }) => {
    const roomId = await createRoom(page, `chat-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    await room.sendMessage('hello from test');
    await expect(room.chatMessages).toContainText('hello from test', { timeout: 5_000 });
  });

  test('message sender name is shown', async ({ authedPage: { page, username } }) => {
    const roomId = await createRoom(page, `sender-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    await room.sendMessage('sender test message');
    await expect(room.chatMessages).toContainText(username, { timeout: 5_000 });
  });

  test('second user sees message via history load', async ({
    authedPage: { page: pageA },
    secondAuthedPage: { page: pageB },
    browser,
  }) => {
    // User A creates a room and sends a message
    const roomId = await createRoom(pageA, `history-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    await roomA.sendMessage('history test message');
    await expect(roomA.chatMessages).toContainText('history test message', { timeout: 5_000 });

    // User B navigates to the same room (they need to join it first since rooms are private by default)
    // Make the room public so User B can access it
    await roomA.page.locator('label:has-text("Public room") input[type=checkbox]').click();
    await pageA.waitForResponse((r) => r.url().includes('/public'));

    // User B navigates to the room via HTMX
    await pageB.goto('/');
    await pageB.waitForSelector('input[name=username]', { state: 'hidden', timeout: 5_000 }).catch(() => {});
    const roomB = await navigateToRoom(pageB, roomId);
    await expect(roomB.chatMessages).toContainText('history test message', { timeout: 5_000 });
  });
});
