import { test, expect } from '../fixtures/auth';
import { createRoom, navigateToRoom } from '../pages/RoomDetailPage';

test.describe('PGP required enforcement', () => {
  test('sending plaintext in PGP-required room shows client error', async ({
    authedPage: { page },
  }) => {
    const roomId = await createRoom(page, `pgp-req-${Date.now()}`);
    const room = await navigateToRoom(page, roomId);
    await room.joinRoom();

    // Enable PGP required without loading any key
    await room.enablePGPRequired();

    // Try to send a message with no PGP key loaded
    await room.chatInput.fill('plaintext should fail');
    await room.chatForm.locator('button[type=submit]').click();

    // Client-side encryption fails because no PGP keys are loaded
    await expect(room.chatMessages).toContainText(
      'Encryption failed: Error encrypting message: No keys, passwords, or session key provided.',
      { timeout: 5_000 }
    );
  });
});
