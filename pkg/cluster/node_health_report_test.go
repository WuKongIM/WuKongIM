package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/observe"
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

func TestReportNodeHealthAllowsLatencyBeyondIntervalWithinLeaseBudget(t *testing.T) {
	controller := newRecordingHealthReportController()
	controller.delay = 50 * time.Millisecond
	node := &Node{
		cfg: Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:7001",
			HealthReport: HealthReportConfig{
				Interval: 20 * time.Millisecond,
				TTL:      400 * time.Millisecond,
			},
		},
		channelDataPlaneLease: newChannelDataPlaneLeaseGuard(time.Now, time.Second),
		snapshot: Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
		},
	}
	node.started.Store(true)
	reporter := observe.NewReporter(observe.ReporterConfig{
		NodeID:       node.cfg.NodeID,
		Addr:         node.cfg.ListenAddr,
		Controller:   controller,
		RuntimeReady: node.runtimeReadyForHealthReport,
	})

	err := node.reportNodeHealth(context.Background(), reporter)

	if err != nil {
		t.Fatalf("reportNodeHealth() error = %v, want nil", err)
	}
	if got := node.channelDataPlaneLease.snapshot().lastVisibleAt; got.IsZero() {
		t.Fatal("lease lastVisibleAt is zero after successful delayed health report")
	}
	if reports := controller.reportsSnapshot(); len(reports) != 1 || !reports[0].RuntimeReady {
		t.Fatalf("reports = %#v, want one ready report", reports)
	}
}

func TestHealthReportTimeoutUsesTTLBackedRenewalBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		ttl      time.Duration
		want     time.Duration
	}{
		{name: "dynamic lifecycle", interval: 500 * time.Millisecond, ttl: 30 * time.Second, want: (30*time.Second - 500*time.Millisecond) / 3},
		{name: "defaults", interval: 5 * time.Second, ttl: 30 * time.Second, want: (30*time.Second - 5*time.Second) / 3},
		{name: "two intervals", interval: time.Second, ttl: 2 * time.Second, want: time.Second},
		{name: "defensive ttl below interval", interval: time.Second, ttl: 500 * time.Millisecond, want: 500 * time.Millisecond},
		{name: "zero values", want: minHealthReportTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := healthReportTimeout(tc.interval, tc.ttl); got != tc.want {
				t.Fatalf("healthReportTimeout(%s, %s) = %s, want %s", tc.interval, tc.ttl, got, tc.want)
			}
		})
	}
}

func TestStopHealthReportLoopReportsNotReadyAfterStopping(t *testing.T) {
	controller := newRecordingHealthReportController()
	node := &Node{
		cfg: Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:7001",
			HealthReport: HealthReportConfig{
				Interval: time.Hour,
				TTL:      50 * time.Millisecond,
			},
		},
		control:               controller,
		channelDataPlaneLease: newChannelDataPlaneLeaseGuard(time.Now, time.Hour),
		snapshot: Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
		},
	}
	node.started.Store(true)
	node.startHealthReportLoop()
	t.Cleanup(node.stopHealthReportLoop)

	initial := controller.waitForReport(t)
	if !initial.RuntimeReady {
		t.Fatalf("initial RuntimeReady = false, want true")
	}
	visibleBeforeStop := node.channelDataPlaneLease.snapshot().lastVisibleAt
	if visibleBeforeStop.IsZero() {
		t.Fatal("channel data-plane lease lastVisibleAt is zero after ready health report")
	}
	node.stopping.Store(true)
	node.stopHealthReportLoop()

	reports := controller.reportsSnapshot()
	if len(reports) < 2 {
		t.Fatalf("health reports = %#v, want final not-ready report after stopping", reports)
	}
	final := reports[len(reports)-1]
	if final.RuntimeReady {
		t.Fatalf("final RuntimeReady = true, want false after stopping; reports=%#v", reports)
	}
	visibleAfterStop := node.channelDataPlaneLease.snapshot().lastVisibleAt
	if !visibleAfterStop.Equal(visibleBeforeStop) {
		t.Fatalf("channel data-plane lease refreshed during not-ready stop report: before=%s after=%s", visibleBeforeStop, visibleAfterStop)
	}
}

func TestReportNodeHealthDoesNotRefreshLeaseWhenReportFails(t *testing.T) {
	reportErr := errors.New("report failed")
	controller := &failingHealthReportController{err: reportErr}
	node := &Node{
		cfg: Config{
			NodeID:     1,
			ListenAddr: "127.0.0.1:7001",
			HealthReport: HealthReportConfig{
				Interval: time.Second,
				TTL:      time.Second,
			},
		},
		channelDataPlaneLease: newChannelDataPlaneLeaseGuard(time.Now, time.Hour),
		snapshot: Snapshot{
			RoutesReady:   true,
			SlotsReady:    true,
			ChannelsReady: true,
		},
	}
	node.started.Store(true)
	reporter := observe.NewReporter(observe.ReporterConfig{
		NodeID:       node.cfg.NodeID,
		Addr:         node.cfg.ListenAddr,
		Controller:   controller,
		RuntimeReady: node.runtimeReadyForHealthReport,
	})

	err := node.reportNodeHealth(context.Background(), reporter)

	if !errors.Is(err, reportErr) {
		t.Fatalf("reportNodeHealth() error = %v, want %v", err, reportErr)
	}
	if got := node.channelDataPlaneLease.snapshot().lastVisibleAt; !got.IsZero() {
		t.Fatalf("lease lastVisibleAt = %s, want zero after failed report", got)
	}
	if len(controller.reports) != 1 || !controller.reports[0].RuntimeReady {
		t.Fatalf("reports = %#v, want one ready report attempt", controller.reports)
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

type recordingHealthReportController struct {
	reports chan control.NodeReport
	delay   time.Duration

	mu     sync.Mutex
	stored []control.NodeReport
}

func newRecordingHealthReportController() *recordingHealthReportController {
	return &recordingHealthReportController{reports: make(chan control.NodeReport, 8)}
}

func (c *recordingHealthReportController) Start(context.Context) error { return nil }

func (c *recordingHealthReportController) Stop(context.Context) error { return nil }

func (c *recordingHealthReportController) LocalSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{}, nil
}

func (c *recordingHealthReportController) LeaderID() uint64 { return 0 }

func (c *recordingHealthReportController) ReportNode(ctx context.Context, report control.NodeReport) error {
	if c.delay > 0 {
		timer := time.NewTimer(c.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.stored = append(c.stored, report)
	c.mu.Unlock()
	select {
	case c.reports <- report:
	default:
	}
	return nil
}

func (c *recordingHealthReportController) ReportSlots(context.Context, control.SlotRuntimeReport) error {
	return nil
}

func (c *recordingHealthReportController) CompleteTask(context.Context, control.TaskResult) error {
	return nil
}

func (c *recordingHealthReportController) FailTask(context.Context, control.TaskResult) error {
	return nil
}

func (c *recordingHealthReportController) ReportTaskProgress(context.Context, control.TaskProgress) error {
	return nil
}

func (c *recordingHealthReportController) AdvanceSlotReplicaMovePhase(context.Context, control.SlotReplicaMovePhaseAdvance) error {
	return nil
}

func (c *recordingHealthReportController) CommitSlotReplicaMove(context.Context, control.SlotReplicaMoveCommit) error {
	return nil
}

func (c *recordingHealthReportController) RequestSlotLeaderTransfer(context.Context, control.SlotLeaderTransferRequest) (control.SlotLeaderTransferResult, error) {
	return control.SlotLeaderTransferResult{}, nil
}

func (c *recordingHealthReportController) RequestSlotReplicaMove(context.Context, control.SlotReplicaMoveRequest) (control.SlotReplicaMoveResult, error) {
	return control.SlotReplicaMoveResult{}, nil
}

func (c *recordingHealthReportController) Watch() <-chan control.SnapshotEvent {
	return nil
}

func (c *recordingHealthReportController) waitForReport(t *testing.T) control.NodeReport {
	t.Helper()
	select {
	case report := <-c.reports:
		return report
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for health report")
		return control.NodeReport{}
	}
}

func (c *recordingHealthReportController) reportsSnapshot() []control.NodeReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]control.NodeReport(nil), c.stored...)
}

type failingHealthReportController struct {
	err     error
	reports []control.NodeReport
}

func (c *failingHealthReportController) ReportNode(_ context.Context, report control.NodeReport) error {
	c.reports = append(c.reports, report)
	return c.err
}

func (c *failingHealthReportController) ReportSlots(context.Context, control.SlotRuntimeReport) error {
	return nil
}
