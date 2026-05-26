package types

import "time"

type Observer interface {
	OnConnectionOpen(event ConnectionEvent)
	OnConnectionClose(event ConnectionEvent)
	OnAuth(event AuthEvent)
	OnFrameIn(event FrameEvent)
	OnFrameOut(event FrameEvent)
	OnFrameHandled(event FrameHandleEvent)
}

type ConnectionEvent struct {
	Listener string
	Network  string
	Protocol string
}

type AuthEvent struct {
	ConnectionEvent
	Status   string
	Duration time.Duration
}

type FrameEvent struct {
	ConnectionEvent
	FrameType string
	Bytes     int
}

type FrameHandleEvent struct {
	ConnectionEvent
	FrameType string
	Duration  time.Duration
	Err       error
}
