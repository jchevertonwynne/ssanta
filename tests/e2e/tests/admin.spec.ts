import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';
import { promoteToAdmin } from '../helpers/admin';

test.describe('admin dashboard', () => {
  test('admin can access admin page and sees users and rooms', async ({
    authedPage: { page: adminPage, username: adminUsername },
    secondAuthedPage: { username: user2Username },
  }) => {
    promoteToAdmin(adminUsername);
    await adminPage.reload();

    await adminPage.goto('/admin');
    await adminPage.waitForLoadState('networkidle');
    await expect(adminPage.locator('.bar:has-text("Admin —")')).toBeVisible({ timeout: 10_000 });

    const usersTable = adminPage.locator('section:has(h2:has-text("Users")) table');
    await expect(usersTable).toContainText(adminUsername);
    await expect(usersTable).toContainText(user2Username);
  });

  test('admin can delete a non-DM room', async ({
    authedPage: { page: adminPage, username: adminUsername },
    secondAuthedPage: { page: creatorPage },
  }) => {
    promoteToAdmin(adminUsername);
    await adminPage.reload();

    const roomName = `admin-del-room-${Date.now()}`;
    const roomId = await createRoom(creatorPage, roomName);
    await navigateToRoom(creatorPage, roomId);

    await adminPage.goto('/admin');
    await adminPage.waitForLoadState('networkidle');
    await expect(adminPage.locator('.bar:has-text("Admin —")')).toBeVisible({ timeout: 10_000 });

    const roomRow = adminPage
      .locator('section:has(h2:has-text("Rooms")) table tbody tr, section:has(h2:has-text("Rooms")) p')
      .filter({ hasText: roomName });
    await expect(roomRow).toBeVisible();

    adminPage.once('dialog', (d) => d.accept());
    await roomRow.locator('button:has-text("Delete")').click();
    await expect(adminPage.locator('section:has(h2:has-text("Rooms"))')).not.toContainText(roomName, { timeout: 10_000 });
  });

  test('admin can delete a user', async ({
    authedPage: { page: adminPage, username: adminUsername },
    secondAuthedPage: { username: user2Username },
  }) => {
    promoteToAdmin(adminUsername);
    await adminPage.reload();

    await adminPage.goto('/admin');
    await adminPage.waitForLoadState('networkidle');
    await expect(adminPage.locator('.bar:has-text("Admin —")')).toBeVisible({ timeout: 10_000 });

    // Use a locator specific to admin.html (has "Admin since" column not in home page users table)
    const adminUsersTable = adminPage.locator('section:has(th:has-text("Admin since")) table');
    const userRow = adminUsersTable.locator('tbody tr').filter({ hasText: user2Username });
    await expect(userRow).toBeVisible();

    adminPage.once('dialog', (d) => d.accept());
    await userRow.locator('button:has-text("Delete")').click();
    await expect(adminUsersTable).not.toContainText(user2Username, { timeout: 10_000 });
  });

  test('admin can grant and revoke admin status', async ({
    authedPage: { page: adminPage, username: adminUsername },
    secondAuthedPage: { username: user2Username },
  }) => {
    promoteToAdmin(adminUsername);
    await adminPage.reload();

    await adminPage.goto('/admin');
    await adminPage.waitForLoadState('networkidle');
    await expect(adminPage.locator('.bar:has-text("Admin —")')).toBeVisible({ timeout: 10_000 });

    // Use a locator specific to admin.html (has "Admin since" column not in home page users table)
    const adminUsersTable = adminPage.locator('section:has(th:has-text("Admin since")) table');

    adminPage.once('dialog', (d) => d.accept());
    await adminUsersTable.locator('tbody tr').filter({ hasText: user2Username }).locator('button:has-text("Grant admin")').click();
    await expect(adminUsersTable.locator('tbody tr').filter({ hasText: user2Username }).locator('button:has-text("Revoke admin")')).toBeVisible({ timeout: 10_000 });

    adminPage.once('dialog', (d) => d.accept());
    await adminUsersTable.locator('tbody tr').filter({ hasText: user2Username }).locator('button:has-text("Revoke admin")').click();
    await expect(adminUsersTable.locator('tbody tr').filter({ hasText: user2Username }).locator('button:has-text("Grant admin")')).toBeVisible({ timeout: 10_000 });
  });

  test('non-admin user does not see Admin button', async ({
    authedPage: { page },
  }) => {
    await page.goto('/');
    await expect(page.locator('button:has-text("Admin")')).toHaveCount(0);
  });
});
