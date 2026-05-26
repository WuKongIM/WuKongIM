package db

import (
	"path/filepath"
	"time"
)

const (
	defaultCommitFlushWindow = 200 * time.Microsecond
	defaultCommitQueueSize   = 1024
)

// CommitOptions controls group-commit batching for coordinated writes.
type CommitOptions struct {
	// FlushWindow is the maximum time spent collecting adjacent commit requests.
	FlushWindow time.Duration
	// QueueSize bounds waiting commit requests before callers apply backpressure.
	QueueSize int
	// MaxRequests caps logical requests per physical commit when positive.
	MaxRequests int
	// MaxRecords caps logical records per physical commit when positive.
	MaxRecords int
	// MaxBytes caps approximate payload bytes per physical commit when positive.
	MaxBytes int
}

// NodeStoreOptions configures the node-local message and metadata stores.
type NodeStoreOptions struct {
	// MessagePath stores channel message logs and message system state.
	MessagePath string
	// MetaPath stores hash-slot metadata rows and indexes.
	MetaPath string
	// Commit controls coordinated write batching.
	Commit CommitOptions
}

// DefaultNodeStoreOptions returns production-shaped defaults under basePath.
func DefaultNodeStoreOptions(basePath string) NodeStoreOptions {
	opts := NodeStoreOptions{
		MessagePath: filepath.Join(basePath, "message"),
		MetaPath:    filepath.Join(basePath, "meta"),
		Commit: CommitOptions{
			FlushWindow: defaultCommitFlushWindow,
			QueueSize:   defaultCommitQueueSize,
		},
	}
	return opts
}

func normalizeNodeStoreOptions(opts NodeStoreOptions) NodeStoreOptions {
	if opts.Commit.FlushWindow == 0 {
		opts.Commit.FlushWindow = defaultCommitFlushWindow
	}
	if opts.Commit.QueueSize <= 0 {
		opts.Commit.QueueSize = defaultCommitQueueSize
	}
	return opts
}
