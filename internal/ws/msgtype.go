package ws

// MsgType is the discriminator field carried in every WebSocket frame.
// Using a named string type prevents raw string literals from being passed
// where a message type is expected.
type MsgType string

const (
	MsgTypeMessage          MsgType = "message"
	MsgTypeSystem           MsgType = "system"
	MsgTypePresence         MsgType = "presence"
	MsgTypeRefresh          MsgType = "refresh"
	MsgTypeKicked           MsgType = "kicked"
	MsgTypeRoomDeleted      MsgType = "room_deleted"
	MsgTypeTyping           MsgType = "typing"
	MsgTypeStoppedTyping    MsgType = "stopped_typing"
	MsgTypeMembershipGained MsgType = "membership_gained"
	MsgTypeMembershipLost   MsgType = "membership_lost"
	MsgTypeUsersUpdated     MsgType = "users_updated"
	MsgTypeRoomsUpdated     MsgType = "rooms_updated"
	MsgTypeInviteReceived   MsgType = "invite_received"
	MsgTypeInviteCancelled  MsgType = "invite_cancelled"
)
