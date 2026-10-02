package protocol

// WebSocketPolicy declares wire requirements independently of product policy.
// A zero value preserves the listener's existing WebSocket behavior.
type WebSocketPolicy struct {
	// Subprotocol, when nonempty, must be offered and selected during Upgrade.
	Subprotocol string
	// BinaryOnly rejects text data messages and makes server data messages binary.
	BinaryOnly bool
}

// WebSocketPolicyProvider optionally constrains a protocol's WebSocket binding.
type WebSocketPolicyProvider interface {
	WebSocketPolicy() WebSocketPolicy
}
