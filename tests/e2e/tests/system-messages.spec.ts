import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';

test.describe('system messages', () => {
  test('join and leave events produce system messages', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    const roomId = await createRoom(pageA, `sysmsg-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    // Make room public so User B can join
    const publicCheckbox = pageA.locator('label:has-text("Public room") input[type=checkbox]');
    if (!(await publicCheckbox.isChecked())) {
      await publicCheckbox.click();
      await pageA.waitForResponse((r) => r.url().includes('/public'));
    }

    // User B joins
    const roomB = await navigateToRoom(pageB, roomId);
    await roomB.joinRoom();

    // User A should see the join system message
    await expect(roomA.chatMessages).toContainText(`${usernameB} joined the room`, { timeout: 5_000 });

    // User B leaves
    await roomB.leaveRoom();

    // User A should see the leave system message
    await expect(roomA.chatMessages).toContainText(`${usernameB} left the room`, { timeout: 5_000 });
  });

  test('removing a member produces a system message', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    const roomId = await createRoom(pageA, `sysmsg-remove-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    const publicCheckbox = pageA.locator('label:has-text("Public room") input[type=checkbox]');
    if (!(await publicCheckbox.isChecked())) {
      await publicCheckbox.click();
      await pageA.waitForResponse((r) => r.url().includes('/public'));
    }

    const roomBFirst = await navigateToRoom(pageB, roomId);
    await roomBFirst.joinRoom();
    await navigateToRoom(pageB, roomId);
    await pageB.waitForTimeout(1_500);

    await expect(pageA.locator('#room-dynamic')).toContainText(usernameB, { timeout: 5_000 });

    await roomA.removeMember(usernameB);

    // User A should see the removal system message
    await expect(roomA.chatMessages).toContainText(`${usernameB} was removed from the room`, { timeout: 5_000 });
  });
});
