package clusterv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

const controllerProbeAttemptTimeout = time.Second

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

// WaitClusterReady waits until all nodes are locally ready and have observed the same control snapshot.
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
		if clusterLocalReady(nodes, latest) {
			return nil
		}
		select {
		case <-ctx.Done():
			return readinessTimeoutError(ctx.Err(), latest)
		case <-ticker.C:
		}
	}
}

// WaitControllerWriteReady waits until cluster snapshots converge and one Controller voter accepts a write probe.
func WaitControllerWriteReady(ctx context.Context, nodes ...*Node) error {
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
	for _, nodeID := range controllerProbeCandidates(nodes, latest) {
		probeSupported, probeReady := controllerProbeNode(ctx, nodes, latest, nodeID)
		if probeSupported {
			supported = true
		}
		if probeReady {
			return true
		}
	}
	return !supported
}

func controllerProbeCandidates(nodes []*Node, latest []nodeReadinessState) []uint64 {
	seen := make(map[uint64]struct{}, len(nodes))
	out := make([]uint64, 0, len(nodes))
	add := func(nodeID uint64) {
		if nodeID == 0 {
			return
		}
		if _, ok := seen[nodeID]; ok {
			return
		}
		seen[nodeID] = struct{}{}
		out = append(out, nodeID)
	}
	for _, node := range nodes {
		if node != nil && node.control != nil {
			add(node.control.LeaderID())
		}
	}
	for _, item := range latest {
		add(item.Snapshot.ControllerLead)
	}
	for _, item := range latest {
		add(item.NodeID)
	}
	return out
}

func controllerProbeNode(ctx context.Context, nodes []*Node, latest []nodeReadinessState, nodeID uint64) (bool, bool) {
	if nodeID == 0 {
		return false, false
	}
	for i, node := range nodes {
		if latest[i].NodeID != nodeID {
			continue
		}
		if node == nil || node.control == nil {
			return false, false
		}
		probe, ok := node.control.(control.ProposeProbe)
		latest[i].ProbeSupported = ok
		if !ok {
			return false, false
		}
		return true, runControllerProbe(ctx, probe, &latest[i])
	}
	return false, false
}

func runControllerProbe(ctx context.Context, probe control.ProposeProbe, latest *nodeReadinessState) bool {
	probeCtx, cancel := context.WithTimeout(ctx, controllerProbeAttemptTimeout)
	err := probe.ProbePropose(probeCtx)
	cancel()
	if err == nil {
		latest.ProbeError = ""
		return true
	}
	latest.ProbeError = err.Error()
	return false
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

func TestWaitControllerWriteReadyReportsControllerProbeTimeout(t *testing.T) {
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
	err = WaitControllerWriteReady(ctx, node)
	if err == nil {
		t.Fatal("WaitControllerWriteReady() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "probe boom") || !strings.Contains(err.Error(), "snapshot=") {
		t.Fatalf("WaitControllerWriteReady() error = %v, want probe and snapshot details", err)
	}
}

func TestWaitControllerWriteReadyRetriesSlowControllerProbe(t *testing.T) {
	controller := &slowThenReadyProbeController{StaticController: control.NewStaticController(nodeControlSnapshot())}
	node, err := New(validNodeConfig(t), withController(controller))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	if err := WaitControllerWriteReady(ctx, node); err != nil {
		t.Fatalf("WaitControllerWriteReady() error = %v", err)
	}
	if calls := controller.calls.Load(); calls < 2 {
		t.Fatalf("ProbePropose() calls = %d, want at least 2", calls)
	}
}

type failingProbeController struct {
	*control.StaticController
	err error
}

func (c *failingProbeController) ProbePropose(context.Context) error { return c.err }

type slowThenReadyProbeController struct {
	*control.StaticController
	calls atomic.Int32
}

func (c *slowThenReadyProbeController) ProbePropose(ctx context.Context) error {
	if c.calls.Add(1) == 1 {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
