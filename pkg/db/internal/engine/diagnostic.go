// Diagnostic-only source copy for Issue #977. No product repair or qualification.
// Events exist only during an operator-owned runtime trace and have a hard cap.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/trace"
	"sync/atomic"
)

var diagnosticGroups atomic.Uint64
var diagnosticEvents atomic.Uint64

// DiagnosticID joins only this owned physical batch; it is never a metric label.
func (b *Batch) DiagnosticID() uint64 {
	if !trace.IsEnabled() {
		return 0
	}
	if b.diagnosticID == 0 {
		b.diagnosticID = diagnosticGroups.Add(1)
	}
	return b.diagnosticID
}
func (b *Batch) diagnosticCommit(phase string) {
	if !trace.IsEnabled() {
		return
	}
	n := diagnosticEvents.Add(1)
	if n > 2048 {
		if n == 2049 {
			trace.Log(context.Background(), "wk977.physical", `{"phase":"cap"}`)
		}
		return
	}
	stats := b.batch.CommitStats()
	data, _ := json.Marshal(map[string]any{"phase": phase, "group": b.DiagnosticID(), "db": fmt.Sprintf("%p", b.db.pdb), "seq": uint64(b.batch.SeqNum()), "total_ns": int64(stats.TotalDuration), "wait_ns": int64(stats.CommitWaitDuration), "semaphore_ns": int64(stats.SemaphoreWaitDuration), "rotation_ns": int64(stats.WALRotationDuration), "wal_queue_ns": int64(stats.WALQueueWaitDuration)})
	trace.Log(context.Background(), "wk977.physical", string(data))
}
