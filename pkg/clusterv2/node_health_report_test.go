package clusterv2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestRuntimeReadyForHealthReportFalseWhileStopping(t *testing.T) {
	node := &Node{snapshot: Snapshot{RoutesReady: true, SlotsReady: true, ChannelsReady: true}}
	node.started.Store(true)
	node.stopping.Store(true)

	if node.runtimeReadyForHealthReport() {
		t.Fatal("runtimeReadyForHealthReport() = true, want false while stopping")
	}
}

func TestHealthReportLoopUsesBoundedReportContext(t *testing.T) {
	controller := newBlockingHealthReportController()
	node := &Node{
		cfg: Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:7001",
			HealthReport: HealthReportConfig{
				Interval: 20 * time.Millisecond,
				TTL:      200 * time.Millisecond,
			},
		},
		control: controller,
		snapshot: Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
		},
	}
	node.started.Store(true)
	node.startHealthReportLoop()
	t.Cleanup(node.stopHealthReportLoop)

	select {
	case <-controller.done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("ReportNode context did not finish before Stop; want bounded per-report timeout")
	}
	if err := controller.lastErr(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReportNode ctx err = %v, want context deadline exceeded", err)
	}
}

type blockingHealthReportController struct {
	done chan struct{}

	mu  sync.Mutex
	err error
}

func newBlockingHealthReportController() *blockingHealthReportController {
	return &blockingHealthReportController{done: make(chan struct{})}
}

func (c *blockingHealthReportController) Start(context.Context) error { return nil }

func (c *blockingHealthReportController) Stop(context.Context) error { return nil }

func (c *blockingHealthReportController) LocalSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{}, nil
}

func (c *blockingHealthReportController) LeaderID() uint64 { return 0 }

func (c *blockingHealthReportController) ReportNode(ctx context.Context, _ control.NodeReport) error {
	<-ctx.Done()
	c.mu.Lock()
	c.err = ctx.Err()
	c.mu.Unlock()
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return ctx.Err()
}

func (c *blockingHealthReportController) ReportSlots(context.Context, control.SlotRuntimeReport) error {
	return nil
}

func (c *blockingHealthReportController) CompleteTask(context.Context, control.TaskResult) error {
	return nil
}

func (c *blockingHealthReportController) FailTask(context.Context, control.TaskResult) error {
	return nil
}

func (c *blockingHealthReportController) ReportTaskProgress(context.Context, control.TaskProgress) error {
	return nil
}

func (c *blockingHealthReportController) AdvanceSlotReplicaMovePhase(context.Context, control.SlotReplicaMovePhaseAdvance) error {
	return nil
}

func (c *blockingHealthReportController) CommitSlotReplicaMove(context.Context, control.SlotReplicaMoveCommit) error {
	return nil
}

func (c *blockingHealthReportController) RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	return control.SlotLeaderTransferResult{}, nil
}

func (c *blockingHealthReportController) RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	return control.SlotReplicaMoveResult{}, nil
}

func (c *blockingHealthReportController) Watch() <-chan control.SnapshotEvent {
	return nil
}

func (c *blockingHealthReportController) lastErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
