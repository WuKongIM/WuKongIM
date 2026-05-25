package db

import "sync/atomic"

// NodeStore is the root handle for node-local storage domains.
type NodeStore struct {
	closed atomic.Bool
	opts   NodeStoreOptions
}

// OpenNodeStore validates options and returns a placeholder root store.
// Pebble engines are wired in a later implementation task.
func OpenNodeStore(opts NodeStoreOptions) (*NodeStore, error) {
	opts = normalizeNodeStoreOptions(opts)
	if opts.MessagePath == "" || opts.MetaPath == "" {
		return nil, ErrInvalidArgument
	}
	return &NodeStore{opts: opts}, nil
}

// Options returns the normalized store options.
func (s *NodeStore) Options() NodeStoreOptions {
	if s == nil {
		return NodeStoreOptions{}
	}
	return s.opts
}

// Close marks the root store closed. Engine shutdown is added later.
func (s *NodeStore) Close() error {
	if s == nil {
		return nil
	}
	s.closed.Store(true)
	return nil
}

// Closed reports whether Close has been called.
func (s *NodeStore) Closed() bool {
	return s == nil || s.closed.Load()
}
