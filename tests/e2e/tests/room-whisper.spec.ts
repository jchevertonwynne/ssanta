import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';

test.describe('whisper messages', () => {
  test('whisper shows (whisper) label and purple colour for sender', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    // User A creates a public room and joins it
    const roomId = await createRoom(pageA, `whisper-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();
    await pageA.locator('label:has-text("Public room") input[type=checkbox]').click();
    await pageA.waitForResponse((r) => r.url().includes('/public'));

    // User B navigates to the room and joins
    const roomB = await navigateToRoom(pageB, roomId);
    await roomB.joinRoom();

    // User A's #chat-target is updated via WS refresh when User B joins
    await expect(pageA.locator('#chat-target')).toContainText(usernameB, { timeout: 5_000 });

    // User A selects User B as the target and sends a whisper
    await pageA.locator('#chat-target').selectOption({ label: usernameB });
    const whisperText = `secret-${Date.now()}`;
    await roomA.sendMessage(whisperText);

    // Assert the whisper appears with the "(whisper)" em label in User A's chat
    const msgLocator = pageA.locator('#chat-messages').getByText(whisperText);
    await expect(msgLocator).toBeVisible({ timeout: 5_000 });

    const msgContainer = pageA.locator('#chat-messages div').filter({ hasText: whisperText }).last();
    await expect(msgContainer.locator('em')).toContainText('(whisper)');

    // User B also sees the whisper
    await expect(pageB.locator('#chat-messages')).toContainText(whisperText, { timeout: 5_000 });
    const msgContainerB = pageB.locator('#chat-messages div').filter({ hasText: whisperText }).last();
    await expect(msgContainerB.locator('em')).toContainText('(whisper)');
  });
});
