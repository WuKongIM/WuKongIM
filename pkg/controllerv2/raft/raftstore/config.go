// Package raftstore persists ControllerV2 Raft state in WAL segments and snapshots.
package raftstore

import (
	"fmt"
	"path/filepath"
)

const (
	defaultWALSegmentSize = uint64(64 << 20)
	metadataVersion       = 1
	snapshotVersion       = 1
)

// Config controls local ControllerV2 Raft durable storage.
type Config struct {
	// Dir is the ControllerV2 Raft storage root directory.
	Dir string
	// NodeID is the local ControllerV2 Raft node ID persisted in WAL segment headers.
	NodeID uint64
	// SegmentSize controls WAL file rollover size in bytes.
	SegmentSize uint64
}

func (c Config) normalized() (Config, error) {
	if c.Dir == "" {
		return Config{}, fmt.Errorf("controllerv2/raftstore: dir is required")
	}
	if c.NodeID == 0 {
		return Config{}, fmt.Errorf("controllerv2/raftstore: node id must be > 0")
	}
	if c.SegmentSize == 0 {
		c.SegmentSize = defaultWALSegmentSize
	}
	abs, err := filepath.Abs(c.Dir)
	if err != nil {
		return Config{}, err
	}
	c.Dir = filepath.Clean(abs)
	return c, nil
}
