package engine

import (
	"time"

	"github.com/cockroachdb/pebble/v2"
)

// NativeCommitStats copies one physical batch's public engine statistics.
// Durations may nest within TotalDuration and are not an additive timeline.
type NativeCommitStats struct {
	// Available requires a non-empty native Commit to have returned successfully.
	// Native rejection and empty no-ops have no sample. A later recovery-seal
	// verification error does not discard the completed native observation.
	Available bool
	// TotalDuration includes native commit work and its internal waits.
	TotalDuration time.Duration
	// SemaphoreWaitDuration measures native commit admission semaphores.
	SemaphoreWaitDuration time.Duration
	// WALQueueWaitDuration is native WAL buffer admission wait. Pebble v2.1.4
	// currently reports zero; an available zero is distinct from a missing sample.
	WALQueueWaitDuration time.Duration
	// MemTableWriteStallDuration measures native memtable pressure stalls.
	MemTableWriteStallDuration time.Duration
	// L0ReadAmpWriteStallDuration measures native L0 read-amplification stalls.
	L0ReadAmpWriteStallDuration time.Duration
	// WALRotationDuration includes syncing/closing the old WAL and opening its replacement.
	WALRotationDuration time.Duration
	// CommitWaitDuration combines sequence publication and WAL sync completion;
	// it does not isolate filesystem sync or device latency.
	CommitWaitDuration time.Duration
}

// CommitStats returns an owned value for the most recent Commit attempt.
// Read it after Commit and before Close, under the batch's existing ownership.
// Missing, rejected and closed batches return an unavailable zero value.
func (b *Batch) CommitStats() NativeCommitStats {
	if b == nil || b.batch == nil {
		return NativeCommitStats{}
	}
	return b.commitStats
}

// commitNative preserves native commit semantics and snapshots its public stats.
// Empty is checked before Commit because large flushable batches detach their
// backing data on success. A returned native error may precede the commit
// pipeline, so it conservatively remains unavailable rather than implying zero.
func (b *Batch) commitNative(opts *pebble.WriteOptions) error {
	empty := b.batch.Empty()
	err := b.batch.Commit(opts)
	if err == nil && !empty {
		stats := b.batch.CommitStats()
		b.commitStats = NativeCommitStats{
			Available:                   true,
			TotalDuration:               stats.TotalDuration,
			SemaphoreWaitDuration:       stats.SemaphoreWaitDuration,
			WALQueueWaitDuration:        stats.WALQueueWaitDuration,
			MemTableWriteStallDuration:  stats.MemTableWriteStallDuration,
			L0ReadAmpWriteStallDuration: stats.L0ReadAmpWriteStallDuration,
			WALRotationDuration:         stats.WALRotationDuration,
			CommitWaitDuration:          stats.CommitWaitDuration,
		}
	}
	return err
}
