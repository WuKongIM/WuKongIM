package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	benchwkproto "github.com/WuKongIM/WuKongIM/internal/bench/wkproto"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestDefaultRunnerCooldownWaitsForReceiveDrainBeforeClosingSessions(t *testing.T) {
	pool := newReceiveDrainWorkerClientPool(1)
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := personShardAssignment()
	assignment.Scenario.Run.Cooldown = 500 * time.Millisecond
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.(AssignmentStopper).EndAssignment(assignment) })

	done := make(chan error, 1)
	go func() { done <- runner.Cooldown(context.Background(), assignment) }()
	select {
	case err := <-done:
		t.Fatalf("Cooldown returned before receive queue convergence: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	pool.setDepth(0)

	require.NoError(t, <-done)
	status := runner.(LifecycleStatusReporter).LifecycleStatus()
	require.True(t, status.ReceiveDrain.TerminalProofComplete(), "%+v", status.ReceiveDrain)
	require.Equal(t, 2, status.ActiveConnections, "receive drain must not close sessions")
	for _, client := range pool.snapshot() {
		require.Zero(t, client.closed, "Cooldown closed a client before terminal evidence")
	}
}

func TestDefaultRunnerCooldownTimeoutPreservesReceiveDrainSnapshot(t *testing.T) {
	pool := newReceiveDrainWorkerClientPool(1)
	runner := NewDefaultWorkloadRunner(pool.newClient)
	assignment := personShardAssignment()
	assignment.Scenario.Run.Cooldown = 25 * time.Millisecond
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { _ = runner.(AssignmentStopper).EndAssignment(assignment) })

	err := runner.Cooldown(context.Background(), assignment)
	require.ErrorContains(t, err, "receive drain exceeded 25ms")
	status := runner.(LifecycleStatusReporter).LifecycleStatus()
	require.False(t, status.ReceiveDrain.DrainComplete)
	require.Equal(t, uint64(2), status.ReceiveDrain.AdapterQueueDepth)
	require.Equal(t, 2, status.ActiveConnections, "failed receive drain must preserve sessions until stop")
}

func TestArchiveReceiveDrainGenerationPermanentlyCarriesIncompleteStoppedCut(t *testing.T) {
	runner := &defaultWorkloadRunner{}
	drained := completeReceiveDrainProof(2500)
	stopped := drained
	stopped.EvidenceComplete = false
	stopped.ActiveDrains = 0

	runner.archiveReceiveDrainGeneration(drained, stopped, false)
	merged := runner.mergeReceiveDrainHistory(completeReceiveDrainProof(2500))

	require.False(t, merged.EvidenceComplete, "an incomplete post-join cut must not be hidden by the next healthy generation")
	require.Equal(t, uint64(2500), merged.ActiveDrains, "historical active drains must not be accumulated")
}

func TestArchiveReceiveDrainGenerationCarriesSuccessfulRecvACKProgress(t *testing.T) {
	runner := &defaultWorkloadRunner{}
	drained := completeReceiveDrainProof(2)
	drained.RecvACKSuccesses = 3
	stopped := drained
	stopped.ActiveDrains = 0
	runner.archiveReceiveDrainGeneration(drained, stopped, false)

	current := completeReceiveDrainProof(2)
	current.RecvACKSuccesses = 2
	merged := runner.mergeReceiveDrainHistory(current)

	require.Equal(t, uint64(5), merged.RecvACKSuccesses)
}

func TestArchiveReceiveDrainGenerationFailsClosedOnRecvACKSuccessOverflow(t *testing.T) {
	runner := &defaultWorkloadRunner{}
	drained := completeReceiveDrainProof(1)
	drained.RecvACKSuccesses = ^uint64(0)
	stopped := drained
	stopped.ActiveDrains = 0
	runner.archiveReceiveDrainGeneration(drained, stopped, false)

	current := completeReceiveDrainProof(1)
	current.RecvACKSuccesses = 1
	merged := runner.mergeReceiveDrainHistory(current)

	require.Equal(t, ^uint64(0), merged.RecvACKSuccesses)
	require.False(t, merged.EvidenceComplete)
}

func TestDefaultRunnerNewAssignmentResetsRecvACKSuccessHistory(t *testing.T) {
	pool := newReceiveDrainWorkerClientPool(0)
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	drained := completeReceiveDrainProof(1)
	drained.RecvACKSuccesses = 7
	stopped := drained
	stopped.ActiveDrains = 0
	runner.archiveReceiveDrainGeneration(drained, stopped, false)

	assignment := personShardAssignment()
	require.NoError(t, runner.Connect(context.Background(), assignment))
	t.Cleanup(func() { require.NoError(t, runner.EndAssignment(assignment)) })

	status := runner.LifecycleStatus()
	require.Zero(t, status.ReceiveDrain.RecvACKSuccesses)
}

type receiveDrainWorkerClientPool struct {
	mu      sync.Mutex
	depth   int64
	clients []*receiveDrainWorkerClient
}

func newReceiveDrainWorkerClientPool(depth int64) *receiveDrainWorkerClientPool {
	return &receiveDrainWorkerClientPool{depth: depth}
}

func (p *receiveDrainWorkerClientPool) newClient(user benchworkload.ConnectionUser, addr string) (benchworkload.ConnectionClient, error) {
	client := &receiveDrainWorkerClient{workerPersonClient: workerPersonClient{uid: user.UID, addr: addr}}
	client.depth.Store(p.depth)
	p.mu.Lock()
	p.clients = append(p.clients, client)
	p.mu.Unlock()
	return client, nil
}

func (p *receiveDrainWorkerClientPool) setDepth(depth int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, client := range p.clients {
		client.depth.Store(depth)
	}
}

func (p *receiveDrainWorkerClientPool) snapshot() []*receiveDrainWorkerClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*receiveDrainWorkerClient(nil), p.clients...)
}

type receiveDrainWorkerClient struct {
	workerPersonClient
	depth atomic.Int64
}

func (c *receiveDrainWorkerClient) QueueSnapshot() benchwkproto.QueueSnapshot {
	depth := int(c.depth.Load())
	return benchwkproto.QueueSnapshot{
		InnerRecvCapacity:   16,
		RecvDepth:           depth,
		RecvCapacity:        16,
		SendackCapacity:     16,
		ErrorCapacity:       16,
		AdapterDepth:        depth,
		AdapterCapacity:     48,
		PublicationCapacity: 16,
	}
}

func completeReceiveDrainProof(clients uint64) model.ReceiveDrainSnapshot {
	return model.ReceiveDrainSnapshot{
		Required:               clients > 0,
		EvidenceComplete:       true,
		DrainComplete:          true,
		ClientCount:            clients,
		ActiveDrains:           clients,
		QueueSnapshotClients:   clients,
		StableZeroObservations: model.ReceiveDrainStableZeroObservations,
	}
}
