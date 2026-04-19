package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jchevertonwynne/ssanta/internal/pgp"
)

func TestCleanupDeletesOldInvitesAndClearsExpiredChallenges(t *testing.T) {
	pool := requireIntegration(t)
	st := New(pool)

	creatorID := createUser(t, pool, "creator")
	inviteeID := createUser(t, pool, "invitee")
	roomID := createRoom(t, pool, "room1", creatorID)

	ctx, cancel := testCtx(t)
	defer cancel()

	// Create an invite and backdate it.
	inviteID := createInvite(t, pool, roomID, creatorID, "invitee")
	_, err := pool.Exec(ctx, `UPDATE room_invites SET expires_at = $1 WHERE id = $2`, time.Now().Add(-time.Hour), inviteID)
	require.NoError(t, err)

	// Create a member with an expired PGP challenge.
	require.NoError(t, st.Rooms.JoinRoom(ctx, roomID, inviteeID))
	armoredPub, _ := mustGenerateTestKeyPair(t)
	normalized, fingerprint, err := pgp.NormalizePublicKey(armoredPub, time.Now())
	require.NoError(t, err)
	challenge := "expired"
	ciphertext, err := pgp.EncryptToPublicKey(normalized, []byte(challenge))
	require.NoError(t, err)
	require.NoError(t, st.Rooms.UpsertRoomUserPGPKeyWithChallenge(ctx, roomID, inviteeID, normalized, fingerprint, ciphertext, pgp.HashChallenge(challenge), time.Now().Add(-time.Minute)))

	deleted, err := st.Invites.DeleteExpiredInvites(ctx, time.Now())
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)

	cleared, err := st.Rooms.ClearExpiredRoomPGPChallenges(ctx, time.Now())
	require.NoError(t, err)
	require.EqualValues(t, 1, cleared)
}
