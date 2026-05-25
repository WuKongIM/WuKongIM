package clusternet

import "errors"

var (
	// ErrNodeNotFound indicates that no peer is registered for a node ID.
	ErrNodeNotFound = errors.New("clusterv2/net: node not found")
	// ErrServiceNotFound indicates that a node has no handler for a service ID.
	ErrServiceNotFound = errors.New("clusterv2/net: service not found")
	// ErrInvalidFrame indicates that a versioned wire frame is malformed or unexpected.
	ErrInvalidFrame = errors.New("clusterv2/net: invalid frame")
)
