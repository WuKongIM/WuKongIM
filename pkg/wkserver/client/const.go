package client

import "fmt"

type ConnectStatus uint32

const (
	UNKNOWN ConnectStatus = iota
	DISCONNECTED
	CONNECTED
	CLOSED
	RECONNECTING
	CONNECTING
	DISCONNECTING
)

func (s ConnectStatus) String() string {
	switch s {
	case UNKNOWN:
		return "UNKNOWN"
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
	case DISCONNECTING:
		return "DISCONNECTING"
	}
	return fmt.Sprintf("unknown status[%d]", s)
}
