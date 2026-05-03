import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';

test.describe('spectator kick', () => {
  test('making a room private kicks non-member spectators', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    // User A creates a room and makes it public
    const roomId = await createRoom(pageA, `spectator-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    const publicCheckbox = pageA.locator('label:has-text("Public room") input[type=checkbox]');
    if (!(await publicCheckbox.isChecked())) {
      await publicCheckbox.click();
      await pageA.waitForResponse((r) => r.url().includes('/public'));
    }

    // User B navigates to the public room as a spectator (does not join)
    await pageB.goto(`/rooms/room_id:${roomId}`);
    await pageB.waitForSelector('#chat-messages', { timeout: 10_000 });
    // Ensure WS is connected
    await pageB.waitForTimeout(1_500);

    // User A makes the room private
    await publicCheckbox.click();
    await pageA.waitForResponse((r) => r.url().includes('/public'));

    // User B should be kicked and redirected to home
    await expect(pageB.locator('h2:has-text("Rooms")')).toBeVisible({ timeout: 10_000 });
    await expect(pageB.locator('.bar')).toContainText(`Logged in as ${usernameB}`);
  });
});
