import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';
import { sendMessages } from '../helpers/messaging';

test.describe('message history pagination', () => {
  test('scrolling to top loads older messages beyond the 50-message page', async ({
    authedPage: { page: pageA },
    secondAuthedPage: { page: pageB },
  }) => {
    const prefix = `pg${Date.now()}`;
    const total = 55;

    // User A creates a public room, joins, and sends 55 messages
    const roomId = await createRoom(pageA, `history-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();
    await pageA.locator('label:has-text("Public room") input[type=checkbox]').click();
    await pageA.waitForResponse((r) => r.url().includes('/public'));

    await sendMessages(roomA, prefix, total);

    // Confirm the last message is visible for User A
    await expect(roomA.chatMessages).toContainText(`${prefix} ${total}`, { timeout: 10_000 });

    // User B enters the room — initial load fetches the newest 50 messages (msgs 6–55)
    const roomB = await navigateToRoom(pageB, roomId);
    await expect(roomB.chatMessages).toContainText(`${prefix} ${total}`, { timeout: 10_000 });

    // Trigger scroll-back: set scrollTop = 0 and fire the scroll event
    await roomB.chatMessages.evaluate((el) => {
      el.scrollTop = 0;
      el.dispatchEvent(new Event('scroll'));
    });

    // Wait for the debounce (200ms) + network fetch to complete and render older messages
    await expect(roomB.chatMessages).toContainText(`${prefix} 1`, { timeout: 5_000 });

    // No duplicate messages — each message text should appear exactly once
    const msg6Text = `${prefix} 6`;
    const count = await roomB.chatMessages.getByText(msg6Text, { exact: true }).count();
    expect(count).toBe(1);
  });
});
