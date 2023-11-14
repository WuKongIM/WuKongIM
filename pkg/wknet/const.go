package wknet

import "errors"

var (
	// ErrUnsupportedOp occurs when calling some methods that has not been implemented yet.
	ErrUnsupportedOp = errors.New("unsupported operation")
)

const (
    ProtocolTCP = "tcp"
    ProtocolWS  = "ws"
)