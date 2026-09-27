package channel

// WillReceipt is immutable publication evidence retained independently of the
// ordinary message body. A store read alone grants no current Channel authority;
// neither a receipt nor its absence authorizes another publication.
type WillReceipt struct {
	MessageID, MessageSeq uint64
	// ServerTimestampMS is the original append time, never a recovery clock.
	ServerTimestampMS int64
	// ContentHash binds UID, client number, body and complete publication metadata
	// using the version-1 receipt's length-delimited SHA-256 format.
	ContentHash [32]byte
}

// Valid rejects incomplete evidence before it can cross a runtime boundary.
func (r WillReceipt) Valid() bool {
	return r.MessageID > 0 && r.MessageSeq > 0 && r.ServerTimestampMS > 0 && r.ContentHash != ([32]byte{})
}
