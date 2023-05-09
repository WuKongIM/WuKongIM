package client

import (
	"errors"

	"go.uber.org/atomic"
)

// Status represents the state of the connection.
type Status int

const (
	DISCONNECTED = Status(iota)
	CONNECTED
	CLOSED
	RECONNECTING
	CONNECTING
)

func (s Status) String() string {
	switch s {
	case DISCONNECTED:
		return "DISCONNECTED"
	case CONNECTED:
		return "CONNECTED"
	case CLOSED:
		return "CLOSED"
	case RECONNECTING:
		return "RECONNECTING"
	case CONNECTING:
		return "CONNECTING"
	}
	return "unknown status"
}

const (
	STALE_CONNECTION = "stale connection"
)

var (
	ErrStaleConnection  = errors.New("limao: " + STALE_CONNECTION)
	ErrNoServers        = errors.New("limao: no servers available for connection")
	ErrBadTimeout       = errors.New("limao: timeout invalid")
	ErrConnectionClosed = errors.New("limao: connection closed")
	ErrTimeout          = errors.New("limao: timeout")
)

type Statistics struct {
	InMsgs     atomic.Uint64
	OutMsgs    atomic.Uint64
	InBytes    atomic.Uint64
	OutBytes   atomic.Uint64
	Reconnects atomic.Uint64
}
