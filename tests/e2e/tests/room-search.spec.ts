import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';

test.describe('room search', () => {
  test('text filter shows only matching messages', async ({ authedPage: { page } }) => {
    const roomId = await createRoom(page, `search-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    await room.sendMessage('hello alpha');
    await room.sendMessage('goodbye beta');
    await expect(room.chatMessages).toContainText('hello alpha');
    await expect(room.chatMessages).toContainText('goodbye beta');

    await room.openSearch();
    await room.searchInput.fill('alpha');
    // Only the matching message should be visible
    await expect(room.chatMessages).toContainText('hello alpha');
    await expect(room.chatMessages).not.toContainText('goodbye beta');
  });

  test('regex filter works', async ({ authedPage: { page } }) => {
    const roomId = await createRoom(page, `regex-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    await room.sendMessage('hello alpha');
    await room.sendMessage('goodbye beta');
    await expect(room.chatMessages).toContainText('goodbye beta');

    await room.openSearch();
    await room.searchRegex.check();
    await room.searchInput.fill('go+dbye');
    await expect(room.chatMessages).toContainText('goodbye beta');
    await expect(room.chatMessages).not.toContainText('hello alpha');
  });

  test('clear button restores all messages', async ({ authedPage: { page } }) => {
    const roomId = await createRoom(page, `clear-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    await room.sendMessage('hello alpha');
    await room.sendMessage('goodbye beta');
    await expect(room.chatMessages).toContainText('goodbye beta');

    await room.openSearch();
    await room.searchInput.fill('alpha');
    await expect(room.chatMessages).not.toContainText('goodbye beta');

    await room.searchClearBtn.click();
    await expect(room.chatMessages).toContainText('hello alpha');
    await expect(room.chatMessages).toContainText('goodbye beta');
  });

  test('sender filter shows only messages from selected sender', async ({
    authedPage: { page, username },
  }) => {
    const roomId = await createRoom(page, `sender-filter-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    await room.sendMessage('my message');
    await expect(room.chatMessages).toContainText('my message');

    await room.openSearch();
    await room.searchSender.selectOption(username);
    await expect(room.chatMessages).toContainText('my message');

    // Selecting a nonexistent sender leaves the list empty or only the one sender visible
    await room.searchSender.selectOption({ label: 'All senders' });
    await expect(room.chatMessages).toContainText('my message');
  });
});
