package delivery

// RecvackCommand records client receive acknowledgement feedback.
type RecvackCommand struct {
	// UID is the user id owning the acknowledged session.
	UID string
	// SessionID is the owner-local session identifier.
	SessionID uint64
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence acknowledged by the client.
	MessageSeq uint64
}

// SessionClosedCommand records a closed online delivery session.
type SessionClosedCommand struct {
	// UID is the user id owning the closed session.
	UID string
	// SessionID is the owner-local session identifier.
	SessionID uint64
}
