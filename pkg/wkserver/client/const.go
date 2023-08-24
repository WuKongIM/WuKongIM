package client

type ConnectStatus uint32

const (
	DISCONNECTED = ConnectStatus(iota)
	CONNECTED
	CLOSED
	RECONNECTING
	CONNECTING
	DISCONNECTING
)

func (s ConnectStatus) String() string {
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
