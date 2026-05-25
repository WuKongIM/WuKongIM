package observe

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestLoopRunsUntilStopped(t *testing.T) {
	calls := make(chan struct{}, 4)
	loop := NewLoop(5*time.Millisecond, func(context.Context) error {
		calls <- struct{}{}
		return nil
	})
	loop.Start(context.Background())
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("loop did not run")
	}
	loop.Stop()
	count := len(calls)
	time.Sleep(20 * time.Millisecond)
	if len(calls) != count {
		t.Fatal("loop continued after Stop")
	}
}

func TestReporterCallsController(t *testing.T) {
	controller := &fakeController{}
	reporter := NewReporter(ReporterConfig{NodeID: 2, Addr: "127.0.0.1:1002", Controller: controller, SlotStatus: func() []SlotStatus { return []SlotStatus{{SlotID: 1, Leader: 2}} }})
	if err := reporter.ReportNode(context.Background()); err != nil {
		t.Fatalf("ReportNode() error = %v", err)
	}
	if err := reporter.ReportSlots(context.Background()); err != nil {
		t.Fatalf("ReportSlots() error = %v", err)
	}
	if controller.node.NodeID != 2 || controller.node.Addr == "" {
		t.Fatalf("node report = %#v, want node 2", controller.node)
	}
	if controller.slots.NodeID != 2 || len(controller.slots.Slots) != 1 {
		t.Fatalf("slot report = %#v, want one slot for node 2", controller.slots)
	}
}

func TestReadinessFromSnapshot(t *testing.T) {
	snap := RuntimeSnapshot{ControlReady: true, RoutesReady: true, SlotsReady: true, ChannelsReady: true}
	if !snap.Ready() {
		t.Fatal("Ready() = false, want true")
	}
	snap.ChannelsReady = false
	if snap.Ready() {
		t.Fatal("Ready() = true, want false")
	}
}

type fakeController struct {
	node  control.NodeReport
	slots control.SlotRuntimeReport
}

func (f *fakeController) ReportNode(_ context.Context, report control.NodeReport) error {
	f.node = report
	return nil
}
func (f *fakeController) ReportSlots(_ context.Context, report control.SlotRuntimeReport) error {
	f.slots = report
	return nil
}
