import { test, expect } from '../fixtures/auth';

test.describe('auth validation errors', () => {
  test('register with mismatched passwords shows error', async ({ page }) => {
    await page.goto('/');
    await page.waitForSelector('input[name=username]');

    await page.fill('input[name=username]', 'mismatchuser');
    await page.fill('input[name=password]', 'Password123!');
    await page.fill('input[name=password_confirm]', 'Different456!');
    await page.click('#register-btn');

    await expect(page.locator('.error')).toContainText('passwords do not match', { timeout: 5_000 });
  });

  test('register with taken username shows error', async ({ authedPage: { username }, browser }) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await page.goto('/');
    await page.waitForSelector('input[name=username]');

    await page.fill('input[name=username]', username);
    await page.fill('input[name=password]', 'Password123!');
    await page.fill('input[name=password_confirm]', 'Password123!');
    await page.click('#register-btn');

    await expect(page.locator('.error')).toContainText('username already taken', { timeout: 5_000 });
    await ctx.close();
  });

  test('register with short password shows error', async ({ page }) => {
    await page.goto('/');
    await page.waitForSelector('input[name=username]');

    await page.fill('input[name=username]', 'shortpassuser');
    await page.fill('input[name=password]', '123');
    await page.fill('input[name=password_confirm]', '123');
    await page.click('#register-btn');

    await expect(page.locator('.error')).toContainText('password must be at least 8 characters', { timeout: 5_000 });
  });

  test('register with invalid username shows error', async ({ page }) => {
    await page.goto('/');
    await page.waitForSelector('input[name=username]');

    await page.fill('input[name=username]', 'ab');
    await page.fill('input[name=password]', 'Password123!');
    await page.fill('input[name=password_confirm]', 'Password123!');
    await page.click('#register-btn');

    await expect(page.locator('.error')).toContainText('username must be 3-32 letters or digits', { timeout: 5_000 });
  });

  test('login with wrong password shows error', async ({ authedPage: { page, username } }) => {
    await page.click('button:has-text("Log out")');
    await page.waitForSelector('#login-btn');

    await page.fill('input[name=username]', username);
    await page.fill('input[name=password]', 'WrongPassword123!');
    await page.click('#login-btn');

    await expect(page.locator('.error')).toContainText('invalid username or password', { timeout: 5_000 });
  });
});
