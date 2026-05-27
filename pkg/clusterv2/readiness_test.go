package clusterv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

type nodeReadinessState struct {
	NodeID         uint64
	Snapshot       Snapshot
	ProbeSupported bool
	ProbeError     string
}

// WaitNodeReady waits until one started node has consumed a valid local control snapshot and runtime gates.
func WaitNodeReady(ctx context.Context, node *Node) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	latest := []nodeReadinessState{{}}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if nodeLocalReady(node, &latest[0]) {
			return nil
		}
		select {
		case <-ctx.Done():
			return readinessTimeoutError(ctx.Err(), latest)
		case <-ticker.C:
		}
	}
}

// WaitClusterReady waits until all nodes are locally ready and one supported Controller voter probe succeeds.
func WaitClusterReady(ctx context.Context, nodes ...*Node) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if len(nodes) == 0 {
		return errors.New("clusterv2 readiness: no nodes")
	}
	latest := make([]nodeReadinessState, len(nodes))
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if clusterLocalReady(nodes, latest) && controllerProbeReady(ctx, nodes, latest) {
			return nil
		}
		select {
		case <-ctx.Done():
			return readinessTimeoutError(ctx.Err(), latest)
		case <-ticker.C:
		}
	}
}

func clusterLocalReady(nodes []*Node, latest []nodeReadinessState) bool {
	var revision uint64
	var slotCount uint32
	var hashSlotCount uint16
	var controllerLead uint64
	for i, node := range nodes {
		if !nodeLocalReady(node, &latest[i]) {
			return false
		}
		snap := latest[i].Snapshot
		if revision == 0 {
			revision = snap.StateRevision
			slotCount = snap.SlotCount
			hashSlotCount = snap.HashSlotCount
			controllerLead = snap.ControllerLead
			continue
		}
		if snap.StateRevision != revision || snap.SlotCount != slotCount || snap.HashSlotCount != hashSlotCount || snap.ControllerLead != controllerLead {
			return false
		}
	}
	return true
}

func nodeLocalReady(node *Node, latest *nodeReadinessState) bool {
	if node == nil {
		latest.NodeID = 0
		latest.Snapshot = Snapshot{}
		return false
	}
	snap := node.Snapshot()
	latest.NodeID = node.NodeID()
	latest.Snapshot = snap
	if !node.started.Load() || node.stopping.Load() {
		return false
	}
	if node.control != nil && (snap.StateRevision == 0 || !snap.RoutesReady) {
		return false
	}
	if !snap.SlotsReady {
		return false
	}
	if node.channels != nil && !snap.ChannelsReady {
		return false
	}
	return true
}

func controllerProbeReady(ctx context.Context, nodes []*Node, latest []nodeReadinessState) bool {
	supported := false
	for i, node := range nodes {
		if node == nil || node.control == nil {
			continue
		}
		probe, ok := node.control.(control.ProposeProbe)
		latest[i].ProbeSupported = ok
		if !ok {
			continue
		}
		supported = true
		err := probe.ProbePropose(ctx)
		if err == nil {
			latest[i].ProbeError = ""
			return true
		}
		latest[i].ProbeError = err.Error()
	}
	return !supported
}

func readinessTimeoutError(cause error, latest []nodeReadinessState) error {
	var b strings.Builder
	fmt.Fprintf(&b, "clusterv2 readiness: %v", cause)
	for _, item := range latest {
		fmt.Fprintf(&b, "\nnode=%d snapshot=%+v probe_supported=%t", item.NodeID, item.Snapshot, item.ProbeSupported)
		if item.ProbeError != "" {
			fmt.Fprintf(&b, " probe_error=%q", item.ProbeError)
		}
	}
	return errors.New(b.String())
}

func TestWaitNodeReadySucceedsForStartedSingleNodeCluster(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Millisecond
	cfg.Control.ClusterID = "readiness-single"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := WaitNodeReady(ctx, node); err != nil {
		t.Fatalf("WaitNodeReady() error = %v", err)
	}
}

func TestWaitClusterReadyReportsControllerProbeTimeout(t *testing.T) {
	probeErr := errors.New("probe boom")
	controller := &failingProbeController{StaticController: control.NewStaticController(nodeControlSnapshot()), err: probeErr}
	node, err := New(validNodeConfig(t), withController(controller))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = WaitClusterReady(ctx, node)
	if err == nil {
		t.Fatal("WaitClusterReady() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "probe boom") || !strings.Contains(err.Error(), "snapshot=") {
		t.Fatalf("WaitClusterReady() error = %v, want probe and snapshot details", err)
	}
}

type failingProbeController struct {
	*control.StaticController
	err error
}

func (c *failingProbeController) ProbePropose(context.Context) error { return c.err }
