import { test, expect } from '../fixtures/auth';

async function submitRoomForm(page: import('@playwright/test').Page): Promise<void> {
  await page.locator('form[hx-post="/rooms"] button[type=submit]').click();
}

test.describe('room creation validation errors', () => {
  test('empty room name shows error', async ({ authedPage: { page } }) => {
    await page.goto('/');
    await page.waitForSelector('#content input[name=display_name]', { timeout: 5_000 });
    // Let any pending WS content updates settle
    await page.waitForTimeout(300);
    await page.evaluate(() => {
      const input = document.querySelector('input[name=display_name]') as HTMLInputElement;
      if (input) {
        input.removeAttribute('required');
        input.value = '   ';
      }
    });
    await submitRoomForm(page);
    await expect(page.locator('#content .error')).toContainText('room name cannot be empty', { timeout: 10_000 });
  });

  test('room name too long shows error', async ({ authedPage: { page } }) => {
    await page.goto('/');
    await page.waitForSelector('#content input[name=display_name]', { timeout: 5_000 });
    await page.waitForTimeout(300);
    // Bypass browser maxlength so the server validates it
    await page.evaluate((val) => {
      const input = document.querySelector('input[name=display_name]') as HTMLInputElement;
      if (input) {
        input.removeAttribute('maxlength');
        input.value = val;
      }
    }, 'a'.repeat(65));
    await submitRoomForm(page);
    await expect(page.locator('#content .error')).toContainText('room name too long', { timeout: 10_000 });
  });

  test('room name with reserved prefix shows error', async ({ authedPage: { page } }) => {
    await page.goto('/');
    await page.waitForSelector('#content input[name=display_name]', { timeout: 5_000 });
    await page.waitForTimeout(300);
    await page.fill('input[name=display_name]', 'dm:something');
    await submitRoomForm(page);
    await expect(page.locator('#content .error')).toContainText('room name cannot use the dm: prefix', { timeout: 10_000 });
  });

  test('duplicate room name shows error', async ({ authedPage: { page } }) => {
    const name = `duplicate-${Date.now()}`;
    await page.goto('/');
    await page.waitForSelector('#content input[name=display_name]', { timeout: 5_000 });
    await page.waitForTimeout(300);
    await page.fill('input[name=display_name]', name);
    await page.locator('form[hx-post="/rooms"] button[type=submit]').click();
    // Wait for the room to appear in the list
    await expect(page.locator(`text=${name}`).first()).toBeVisible({ timeout: 5_000 });

    // Try creating again with the same name
    await page.fill('input[name=display_name]', name);
    await page.locator('form[hx-post="/rooms"] button[type=submit]').click();
    await expect(page.locator('#content .error')).toContainText('room name already taken', { timeout: 10_000 });
  });
});
