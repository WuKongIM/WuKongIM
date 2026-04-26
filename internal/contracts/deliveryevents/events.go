// Package deliveryevents defines delivery usecase event contracts.
package deliveryevents

// RouteAck records that a receiver acknowledged a delivered message route.
type RouteAck struct {
	UID        string
	SessionID  uint64
	MessageID  uint64
	MessageSeq uint64
}

// SessionClosed records that a user session closed and pending routes should be cleaned up.
type SessionClosed struct {
	UID       string
	SessionID uint64
}
