//go:build integration

package worker

import (
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/stretchr/testify/require"
)

// This uses a real bounded wait to establish that assignment replacement cannot
// pass a deliberately blocked snapshot. It belongs in the integration tier.
func TestAssignmentEvidenceCaptureFencesReplacement(t *testing.T) {
	runner := &blockedEvidenceRunner{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runner.release) }) }
	defer release()
	owner, identity := assignedLifecycle(t, runner)
	stop := owner.Stop(identity)
	<-stop.done
	require.NoError(t, stop.err)
	type evidenceResult struct {
		evidence assignmentEvidence
		err      error
	}
	captured := make(chan evidenceResult, 1)
	go func() { evidence, err := owner.Evidence(identity); captured <- evidenceResult{evidence, err} }()
	select {
	case <-runner.entered:
	case <-time.After(time.Second):
		t.Fatal("evidence collection did not enter runner")
	}
	attempted := make(chan struct{})
	replaced := make(chan error, 1)
	go func() {
		close(attempted)
		_, err := owner.Assign(Assignment{RunID: identity.runID, AssignmentID: "replacement", WorkerID: "worker-a"})
		replaced <- err
	}()
	<-attempted
	select {
	case err := <-replaced:
		t.Fatalf("replacement crossed in-progress evidence capture: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	var result evidenceResult
	select {
	case result = <-captured:
	case <-time.After(time.Second):
		t.Fatal("evidence capture did not finish")
	}
	require.NoError(t, result.err)
	select {
	case err := <-replaced:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("replacement did not resume")
	}
	require.Equal(t, identity.assignmentID, result.evidence.status.Assignment.AssignmentID)
	require.Equal(t, uint64(1), result.evidence.metrics.Counters["connect_success_total"], "returned evidence changed with replacement")
	_, err := owner.Evidence(identity)
	require.ErrorIs(t, err, ErrActiveRunConflict)
}

// The runner intentionally reuses its counters to check the module's detached,
// sanitized snapshot as well as its assignment admission fence.
type blockedEvidenceRunner struct {
	snapshotRunner
	entered    chan struct{}
	release    chan struct{}
	generation uint64
}

func (r *blockedEvidenceRunner) BeginAssignment(Assignment) {
	r.generation++
	if r.metrics.Counters == nil {
		r.metrics.Counters = map[string]uint64{}
	}
	r.metrics.Counters["connect_success_total"] = r.generation
}
func (r *blockedEvidenceRunner) MetricsSnapshot() metrics.SnapshotData {
	close(r.entered)
	<-r.release
	return r.metrics
}
