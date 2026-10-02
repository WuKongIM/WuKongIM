package channel

import "context"

// MQTTReplayCopier confirms bounded immutable shared coverage on the current
// leader and a strict current-voter quorum. It does not authorize source release.
type MQTTReplayCopier interface {
	CopyMQTTReplay(context.Context, MQTTReplayRequest) (MQTTReplayCopyReceipt, error)
}

// MQTTReplayCopyReceipt records transient, current-authority copy evidence.
// Consumers must replicate an accepted decision before using it for source GC
// or recovery anchors. Copies contains distinct sorted current ISR identities.
type MQTTReplayCopyReceipt struct {
	Request MQTTReplayRequest
	Leader  NodeID
	// Authority binds exact placement, epochs, route, leader, status and quorum.
	Authority     [32]byte
	WriteQuorum   int
	Before, After MQTTReplayPrefix
	Copies        []NodeID
}
