import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';
import { PASSWORD } from '../fixtures/auth';

test.describe('room management', () => {
  test('non-creator member can leave a room and is redirected to home', async ({
    authedPage: { page: pageA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    const roomId = await createRoom(pageA, `leave-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();
    await pageA.locator('label:has-text("Public room") input[type=checkbox]').click();
    await pageA.waitForResponse((r) => r.url().includes('/public'));

    // User B joins via HTMX click, then re-navigates for a clean page with working WS
    const roomBFirst = await navigateToRoom(pageB, roomId);
    await roomBFirst.joinRoom();
    const roomB = await navigateToRoom(pageB, roomId);

    // User B leaves — non-creator gets hx-push-url="/" and content swapped to home
    await roomB.leaveRoom();
    await expect(pageB.locator('h2:has-text("Rooms")')).toBeVisible({ timeout: 10_000 });
    await expect(pageB.locator('.bar')).toContainText(`Logged in as ${usernameB}`);
  });

  test('creator can delete a room and is redirected to home', async ({
    authedPage: { page: pageA, username: usernameA },
  }) => {
    const roomId = await createRoom(pageA, `delete-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    await roomA.deleteRoom();
    // After delete, HTMX swaps #content with home page (hx-push-url="/")
    await expect(pageA.locator('h2:has-text("Rooms")')).toBeVisible({ timeout: 10_000 });
    await expect(pageA.locator('.bar')).toContainText(`Logged in as ${usernameA}`);
  });

  test('creator can remove a member from the room', async ({
    authedPage: { page: pageA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    const roomId = await createRoom(pageA, `removemember-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();
    await pageA.locator('label:has-text("Public room") input[type=checkbox]').click();
    await pageA.waitForResponse((r) => r.url().includes('/public'));

    // User B joins via HTMX, then re-navigates for a clean WS connection
    const roomBFirst = await navigateToRoom(pageB, roomId);
    await roomBFirst.joinRoom();
    await navigateToRoom(pageB, roomId);
    // Give the WS handshake time to complete so User B receives the 'kicked' event
    await pageB.waitForTimeout(1500);

    // User A's #room-dynamic should reflect User B's membership (from the join WS refresh)
    await expect(pageA.locator('#room-dynamic')).toContainText(usernameB, { timeout: 5_000 });

    // User A removes User B
    await roomA.removeMember(usernameB);

    // User B receives the 'kicked' WS event → redirected to home page
    await expect(pageB.locator('h2:has-text("Rooms")')).toBeVisible({ timeout: 10_000 });

    // User B no longer appears in User A's member list
    await expect(pageA.locator('#room-dynamic')).not.toContainText(usernameB, { timeout: 5_000 });
  });

  test('user can change their account password', async ({
    authedPage: { page, username },
  }) => {
    await page.goto('/');

    await page.locator('[data-action="show-change-password-form"]').click();

    const newPassword = 'NewPassword456!';
    await page.locator('input[name=current_password]').last().fill(PASSWORD);
    await page.locator('input[name=new_password]').fill(newPassword);
    await page.locator('input[name=new_password_confirm]').fill(newPassword);
    await page.locator('form[hx-post="/password"] button[type=submit]').click();

    await expect(page.locator('.success')).toContainText('Password changed successfully.', { timeout: 5_000 });
  });

  test('members can invite when setting is enabled', async ({
    authedPage: { page: pageA, username: usernameA },
    secondAuthedPage: { page: pageB, username: usernameB },
  }) => {
    // We need a third user to be the invitee
    const thirdCtx = await pageA.context().browser()!.newContext();
    const pageC = await thirdCtx.newPage();
    const usernameC = 'u' + Date.now().toString(36) + Math.random().toString(36).slice(2, 5);
    await pageC.goto('/');
    await pageC.waitForSelector('input[name=username]');
    await pageC.fill('input[name=username]', usernameC);
    await pageC.fill('input[name=password]', PASSWORD);
    await pageC.fill('input[name=password_confirm]', PASSWORD);
    await pageC.click('#register-btn');
    await expect(pageC.locator('.bar')).toContainText(`Logged in as ${usernameC}`, { timeout: 10_000 });

    // User A creates a room, joins, and enables "Members can invite"
    const roomId = await createRoom(pageA, `members-invite-${Date.now()}`);
    const roomA = await navigateToRoom(pageA, roomId);
    await roomA.joinRoom();

    // Enable members-can-invite
    const membersCanInviteCheckbox = pageA.locator('label:has-text("Members can invite") input[type=checkbox]');
    if (!(await membersCanInviteCheckbox.isChecked())) {
      const mciDone = pageA.waitForResponse((r) => r.url().includes('/members-can-invite'));
      await membersCanInviteCheckbox.click();
      await mciDone;
    }

    // Make room public so User B can join
    const publicDone = pageA.waitForResponse((r) => r.url().includes('/public'));
    await pageA.locator('label:has-text("Public room") input[type=checkbox]').click();
    await publicDone;

    // User B joins the room
    const roomBFirst = await navigateToRoom(pageB, roomId);
    await roomBFirst.joinRoom();
    const roomB = await navigateToRoom(pageB, roomId);

    // User B should now see the invite form and be able to invite User C
    await expect(roomB.page.locator('select[name=invitee_username]')).toBeVisible({ timeout: 5_000 });
    await roomB.page.locator('select[name=invitee_username]').selectOption({ value: usernameC });
    await roomB.page.locator('button:has-text("Send invite")').click();
    await roomB.page.waitForResponse((r) => r.url().includes('/invites'));

    // User C should see the invite on their home page (invite was sent by User B)
    await pageC.goto('/');
    await expect(pageC.locator('#content-invites')).toContainText(usernameB, { timeout: 5_000 });

    await thirdCtx.close();
  });
});
