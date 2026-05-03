import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';

test.describe('typing indicator', () => {
  test('typing indicator visible to other users while typing', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB },
  }) => {
    // User A creates a public room
    const roomId = await createRoom(pageA, `typing-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    // Make the room public so User B can see it
    const publicCheckbox = pageA.locator('label:has-text("Public room") input[type=checkbox]');
    if (!(await publicCheckbox.isChecked())) {
      await publicCheckbox.click();
      await pageA.waitForResponse((r) => r.url().includes('/public'));
    }

    // User B navigates to the room
    await pageB.goto('/');
    const roomB = await navigateToRoom(pageB, roomId);

    // User A starts typing — the first input event triggers a WS "typing" frame
    await roomA.chatInput.fill('t');

    // User B should see the typing indicator within a few seconds
    await expect(roomB.typingIndicator).toContainText('is typing', { timeout: 5_000 });
  });

  test('typing indicator disappears after debounce', async ({
    authedPage: { page: pageA },
    secondAuthedPage: { page: pageB },
  }) => {
    const roomId = await createRoom(pageA, `typing-debounce-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    const publicCheckbox = pageA.locator('label:has-text("Public room") input[type=checkbox]');
    if (!(await publicCheckbox.isChecked())) {
      await publicCheckbox.click();
      await pageA.waitForResponse((r) => r.url().includes('/public'));
    }

    await pageB.goto('/');
    const roomB = await navigateToRoom(pageB, roomId);

    await roomA.chatInput.fill('typing…');
    await expect(roomB.typingIndicator).toContainText('is typing', { timeout: 5_000 });

    // After the debounce window (4000ms), the indicator should clear
    await expect(roomB.typingIndicator).toHaveText('', { timeout: 6_000 });
  });
});
