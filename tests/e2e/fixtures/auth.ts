import { test as base, expect, Browser, Page } from '@playwright/test';
import { promoteToAdmin } from '../helpers/admin';

const PASSWORD = 'TestPassword123!';

export type AuthedUser = { page: Page; username: string };

export type AuthFixtures = {
  authedPage: AuthedUser;
  secondAuthedPage: AuthedUser;
  adminAuthedPage: AuthedUser;
};

// Generates a short, valid username unique per call.
function uniqueUsername(): string {
  return 'u' + Date.now().toString(36) + Math.random().toString(36).slice(2, 5);
}

async function registerUser(page: Page, username: string): Promise<void> {
  await page.goto('/');
  // Wait for HTMX to load /content into #content
  await page.waitForSelector('input[name=username]');
  await page.fill('input[name=username]', username);
  await page.fill('input[name=password]', PASSWORD);
  await page.fill('input[name=password_confirm]', PASSWORD);
  await page.click('#register-btn');
  await expect(page.locator('.bar')).toContainText(`Logged in as ${username}`, { timeout: 10_000 });
}

export const test = base.extend<AuthFixtures & { browser: Browser }>({
  authedPage: async ({ page }, use) => {
    const username = uniqueUsername();
    await registerUser(page, username);
    await use({ page, username });
  },

  secondAuthedPage: async ({ browser }, use) => {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const username = uniqueUsername();
    console.log('secondAuthedPage context:', (ctx as any)._guid || 'no-guid', 'page:', (page as any)._guid || 'no-guid');
    await registerUser(page, username);
    await use({ page, username });
    await ctx.close();
  },

  adminAuthedPage: async ({ page, secondAuthedPage: _ }, use) => {
    const username = uniqueUsername();
    console.log('adminAuthedPage context:', (page.context() as any)._guid || 'no-guid', 'page:', (page as any)._guid || 'no-guid');
    await registerUser(page, username);
    promoteToAdmin(username);
    await use({ page, username });
  },
});

export { expect };
export { PASSWORD };
