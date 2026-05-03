import { Page, expect, Locator } from '@playwright/test';

export class RoomDetailPage {
  readonly page: Page;

  // Chat
  readonly chatMessages: Locator;
  readonly chatInput: Locator;
  readonly chatForm: Locator;
  readonly typingIndicator: Locator;

  // Search
  readonly searchWrapper: Locator;
  readonly searchInput: Locator;
  readonly searchRegex: Locator;
  readonly searchSender: Locator;
  readonly searchClearBtn: Locator;

  // PGP (elements live in #room-sidebar, JS is in room_detail.html)
  readonly pgpSection: Locator;
  readonly pgpPrivateKeyInput: Locator;
  readonly pgpPassphraseInput: Locator;
  readonly pgpPersistToggle: Locator;
  readonly pgpLoadKeyBtn: Locator;
  readonly pgpKeyStatus: Locator;

  // Room settings (sidebar)
  readonly pgpRequiredCheckbox: Locator;

  constructor(page: Page) {
    this.page = page;
    this.chatMessages = page.locator('#chat-messages');
    this.chatInput = page.locator('#chat-input');
    this.chatForm = page.locator('#chat-form');
    this.typingIndicator = page.locator('#typing-indicator');
    this.searchWrapper = page.locator('#search-bar-wrapper');
    this.searchInput = page.locator('#search-input');
    this.searchRegex = page.locator('#search-regex');
    this.searchSender = page.locator('#search-sender');
    this.searchClearBtn = page.locator('#search-clear-btn');
    this.pgpSection = page.locator('#pgp-keys-section');
    this.pgpPrivateKeyInput = page.locator('#pgp-private-key-input');
    this.pgpPassphraseInput = page.locator('#pgp-passphrase-input');
    this.pgpPersistToggle = page.locator('#pgp-persist-toggle');
    this.pgpLoadKeyBtn = page.locator('#pgp-load-key-btn');
    this.pgpKeyStatus = page.locator('#pgp-key-status');
    this.pgpRequiredCheckbox = page.locator('label:has-text("Require PGP encryption") input[type=checkbox]');
  }

  async waitForReady(): Promise<void> {
    await expect(this.chatMessages).toBeVisible({ timeout: 10_000 });
  }

  async sendMessage(text: string): Promise<void> {
    await this.chatInput.fill(text);
    await this.chatForm.locator('button[type=submit]').click();
    await expect(this.chatInput).toHaveValue('');
  }

  async openSearch(): Promise<void> {
    const isClosed = !(await this.searchWrapper.evaluate((el) => (el as HTMLDetailsElement).open));
    if (isClosed) {
      await this.searchWrapper.locator('summary').click();
    }
  }

  async loadPGPKey(armoredPrivateKey: string, passphrase = ''): Promise<void> {
    // Open the PGP section if collapsed
    const isOpen = await this.pgpSection.evaluate((el) => (el as HTMLDetailsElement).open);
    if (!isOpen) {
      await this.pgpSection.locator('summary').first().click();
    }
    await this.pgpPrivateKeyInput.fill(armoredPrivateKey);
    if (passphrase) {
      await this.pgpPassphraseInput.fill(passphrase);
    }
    await this.pgpLoadKeyBtn.click();
    await expect(this.pgpKeyStatus).toContainText('Key loaded. Fingerprint:', { timeout: 10_000 });
  }

  async enablePGPRequired(): Promise<void> {
    const isChecked = await this.pgpRequiredCheckbox.isChecked();
    if (!isChecked) {
      await this.pgpRequiredCheckbox.click();
      // HTMX reloads #room-sidebar on change
      await this.page.waitForResponse((r) => r.url().includes('/pgp-required'));
    }
  }

  async joinRoom(): Promise<void> {
    const joinBtn = this.page.locator('button:has-text("Join room")');
    try {
      await joinBtn.waitFor({ state: 'visible', timeout: 3_000 });
    } catch {
      // Button not present — already a member
      return;
    }
    await joinBtn.click();
    await this.page.waitForResponse((r) => r.url().includes('/join'));
    // After joining, the chat input should become enabled
    await expect(this.chatInput).toBeEnabled({ timeout: 5_000 });
    // Give HTMX a moment to finish processing the swapped sidebar.
    await this.page.waitForTimeout(500);
  }

  async leaveRoom(): Promise<void> {
    this.page.once('dialog', (d) => d.accept());
    await this.page.locator('button:has-text("Leave room")').click();
  }

  async deleteRoom(): Promise<void> {
    this.page.once('dialog', (d) => d.accept());
    await this.page.locator('button:has-text("Delete room")').click();
  }

  async removeMember(username: string): Promise<void> {
    this.page.once('dialog', (d) => d.accept());
    const memberLi = this.page.locator(`#room-dynamic li`).filter({ hasText: username });
    await memberLi.locator('button:has-text("Remove User")').click();
    await this.page.waitForResponse((r) => r.url().includes('/members/'));
  }
}

// Navigate to a room via a full page load (avoids stale content WebSocket issues).
export async function navigateToRoom(page: Page, roomId: number): Promise<RoomDetailPage> {
  await page.goto(`/rooms/room_id:${roomId}`);
  const room = new RoomDetailPage(page);
  await room.waitForReady();
  // Give HTMX a moment to finish processing hx-* attributes in the swapped sidebar.
  await page.waitForTimeout(300);
  return room;
}

// Create a room via the UI and return its ID.
export async function createRoom(page: Page, displayName: string): Promise<number> {
  await page.fill('input[name=display_name]', displayName);
  await page.locator('form[hx-post="/rooms"] button[type=submit]').click();
  // After creation, HTMX swaps the content. The new room should appear in the list.
  // Extract the room ID from the "Enter" button's hx-get attribute.
  const enterBtn = page.locator(`text=${displayName}`).locator('..').locator('button:has-text("Enter"), button:has-text("View")').first();
  await expect(enterBtn).toBeVisible({ timeout: 5_000 });
  const hxGet = await enterBtn.getAttribute('hx-get');
  if (!hxGet) throw new Error(`Could not find Enter button for room "${displayName}"`);
  const match = hxGet.match(/\/rooms\/(?:room_id:)?(\d+)/);
  if (!match) throw new Error(`Unexpected hx-get value: ${hxGet}`);
  return parseInt(match[1], 10);
}
