package server

import "fmt"

var (
	ErrConnNotFound   = fmt.Errorf("conn not found")
	ErrReactorStopped = fmt.Errorf("reactor stopped")
)
