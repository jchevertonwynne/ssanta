import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';
import { promoteToAdmin } from '../helpers/admin';

test.describe('admin read-only view', () => {
  test('admin can view a private room they are not a member of', async ({
    authedPage: { page: adminPage, username: adminUsername },
    secondAuthedPage: { page: creatorPage },
  }) => {
    promoteToAdmin(adminUsername);
    await adminPage.reload();

    // Creator makes a private room
    const roomName = `admin-view-${Date.now()}`;
    const roomId = await createRoom(creatorPage, roomName);
    const creatorRoom = await navigateToRoom(creatorPage, roomId);
    await creatorRoom.joinRoom();
    // Ensure room is private
    const publicCheckbox = creatorPage.locator('label:has-text("Public room") input[type=checkbox]');
    if (await publicCheckbox.isChecked()) {
      await publicCheckbox.click();
      await creatorPage.waitForResponse((r) => r.url().includes('/public'));
    }

    // Admin navigates directly to the room
    await adminPage.goto(`/rooms/room_id:${roomId}`);
    await adminPage.waitForSelector('#chat-messages', { timeout: 10_000 });

    // Should see the admin view banner
    await expect(adminPage.locator('h4:has-text("Admin View - non interactive")')).toBeVisible({ timeout: 5_000 });

    // Should NOT see interactive controls
    await expect(adminPage.locator('button:has-text("Join room")')).toHaveCount(0);
    await expect(adminPage.locator('button:has-text("Leave room")')).toHaveCount(0);
    await expect(adminPage.locator('button:has-text("Delete room")')).toHaveCount(0);
    await expect(adminPage.locator('#pgp-keys-section')).toHaveCount(0);
  });
});
