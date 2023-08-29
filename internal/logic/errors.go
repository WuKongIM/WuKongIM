package logic

import "errors"

var (
	ErrGatewayNotFound  = errors.New("gateway not found")
	ErrNodeConnNotFound = errors.New("node conn not found")
)
