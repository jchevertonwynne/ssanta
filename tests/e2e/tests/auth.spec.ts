import { test, expect } from '../fixtures/auth';

test.describe('auth', () => {
  test('register then shows logged-in state', async ({ authedPage: { page, username } }) => {
    await expect(page.locator('.bar')).toContainText(`Logged in as ${username}`);
  });

  test('logout returns to login form', async ({ authedPage: { page } }) => {
    await page.click('button:has-text("Log out")');
    await expect(page.locator('#login-btn')).toBeVisible({ timeout: 5_000 });
  });

  test('login with existing credentials', async ({ authedPage: { page, username } }) => {
    // Log out first
    await page.click('button:has-text("Log out")');
    await page.waitForSelector('#login-btn');

    // Clear password_confirm (not needed for login) and log back in
    await page.fill('input[name=username]', username);
    await page.fill('input[name=password]', 'TestPassword123!');
    await page.fill('input[name=password_confirm]', '');
    await page.click('#login-btn');
    await expect(page.locator('.bar')).toContainText(`Logged in as ${username}`, { timeout: 10_000 });
  });

  test('user can delete their own account', async ({ authedPage: { page, username } }) => {
    await page.goto('/');

    await page.locator('button[data-action="show-delete-form"]').click();
    await page.locator('#delete-account-ui input[name=current_password]').fill('TestPassword123!');

    page.once('dialog', (d) => d.accept());
    await page.locator('#delete-account-ui form button[type=submit]').click();

    // After deletion, should be back at login form
    await expect(page.locator('#login-btn')).toBeVisible({ timeout: 10_000 });

    // Reload to get a fresh CSRF token for the anonymous session
    await page.reload();
    await page.waitForSelector('#login-btn');

    // Trying to log in with old credentials should fail
    await page.fill('input[name=username]', username);
    await page.fill('input[name=password]', 'TestPassword123!');
    await page.click('#login-btn');
    await expect(page.locator('.error')).toContainText('invalid username or password', { timeout: 10_000 });
  });
});
