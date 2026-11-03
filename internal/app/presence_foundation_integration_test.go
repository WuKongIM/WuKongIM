//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// foundationEntry observes the same Start seam used by the HTTP entry runtime.
// It never issues a lookup that could itself prepare the generation under test.
type foundationEntry struct {
	node            *generationWiringCluster
	t               *testing.T
	allowIncomplete bool
	starts          atomic.Int32
	directory       *authority.Directory
	target          presence.RouteTarget
}

func (e *foundationEntry) Start() error {
	e.starts.Add(1)
	e.node.mu.Lock()
	g := e.node.claim
	e.node.mu.Unlock()
	if g.Generation == 0 && !e.allowIncomplete {
		e.t.Error("business admission opened before its local Presence foundation")
	}
	if !e.allowIncomplete && e.directory != nil {
		if _, complete, err := e.directory.CurrentGeneration(e.target); err != nil || !complete {
			e.t.Errorf("business entry opened before complete owner proof: complete=%v err=%v", complete, err)
		}
	}
	return nil
}

// foundationFaultCluster faults the existing remote-owned generation port;
// application startup and all recovery scheduling remain production code.
type foundationFaultCluster struct {
	*generationWiringCluster
	ensure    func(context.Context) error
	claimHook func(context.Context)
	claims    atomic.Int32
	stops     atomic.Int32
}

func (n *foundationFaultCluster) EnsurePresenceOwnerSet(ctx context.Context) (controller.PresenceOwnerSet, error) {
	if n.ensure != nil {
		if err := n.ensure(ctx); err != nil {
			return controller.PresenceOwnerSet{}, err
		}
	}
	return n.generationWiringCluster.EnsurePresenceOwnerSet(ctx)
}
func (n *foundationFaultCluster) ClaimPresenceGeneration(ctx context.Context, req controller.ClaimPresenceGenerationRequest) (controller.PresenceGeneration, error) {
	n.claims.Add(1)
	if n.claimHook != nil {
		n.claimHook(ctx)
	}
	return n.generationWiringCluster.ClaimPresenceGeneration(ctx, req)
}
func (n *foundationFaultCluster) Stop(ctx context.Context) error {
	n.stops.Add(1)
	return n.generationWiringCluster.Stop(ctx)
}

func newFoundationFaultNode(leader uint64) *foundationFaultCluster {
	base := newFakePresenceCluster(1, nil)
	base.snapshot = readyFakeClusterSnapshot(1, 1)
	return &foundationFaultCluster{generationWiringCluster: &generationWiringCluster{
		recoveryWiringCluster: &recoveryWiringCluster{base},
		route:                 clusterpkg.Route{HashSlot: 0, SlotID: 11, Leader: leader, LeaderTerm: 23, ConfigEpoch: 29, Revision: 17, AuthorityEpoch: 19},
	}}
}

func TestPresenceFoundationUnavailableRemainsPartial(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsupported_owner", true: "remote_leader"}[remote], func(t *testing.T) {
			leader := uint64(1)
			if remote {
				leader = 2
			}
			node := newFoundationFaultNode(leader)
			var ensures atomic.Int32
			node.ensure = func(context.Context) error { ensures.Add(1); return controller.ErrPresenceGenerationNotReady }
			entry := &foundationEntry{node: node.generationWiringCluster, t: t, allowIncomplete: true}
			a, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}}, WithCluster(node), WithAPI(entry), WithGateway(nil))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			defer func() {
				if err := a.Stop(ctx); err != nil {
					t.Error(err)
				}
			}()
			if err := a.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if entry.starts.Load() != 1 {
				t.Fatal("partial preparation prevented independent control/business entry startup")
			}
			want := int32(1)
			if remote {
				want = 0
			}
			if ensures.Load() != want {
				t.Fatalf("preparation retried or claimed a remote authority: calls=%d want=%d", ensures.Load(), want)
			}
			if node.claims.Load() != 0 {
				t.Fatal("missing owner evidence produced a generation")
			}
		})
	}
}

func TestPresenceFoundationCanceledLateProviderRetainsDependencies(t *testing.T) {
	node := newFoundationFaultNode(1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	node.claimHook = func(context.Context) { close(entered); <-release }
	entry := &foundationEntry{node: node.generationWiringCluster, t: t}
	a, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}}, WithCluster(node), WithAPI(entry), WithGateway(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		once.Do(func() { close(release) })
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := a.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Start(ctx) }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("startup ended without generation preparation: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not reach generation provider")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled startup=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("startup cancellation waited for uncooperative provider")
	}
	if entry.starts.Load() != 0 || node.stops.Load() != 0 {
		t.Fatal("canceled preparation opened entries or released its provider dependency")
	}
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if err := a.Stop(short); err == nil {
		t.Error("Stop reported joined while generation provider was still executing")
	}
	stop()
	if node.stops.Load() != 0 {
		t.Fatal("incomplete Stop closed the cluster")
	}
	once.Do(func() { close(release) })
	joined, joinCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer joinCancel()
	if err := a.Stop(joined); err != nil {
		t.Fatal(err)
	}
	if node.stops.Load() != 1 {
		t.Fatalf("dependency close count=%d", node.stops.Load())
	}
}
func (*foundationEntry) Stop(context.Context) error { return nil }
func (*foundationEntry) Addr() string               { return "" }

type foundationManyRoutes struct{ *foundationFaultCluster }

type foundationLostRoute struct {
	*foundationFaultCluster
	lost atomic.Bool
}

func (n *foundationLostRoute) ProbeWriteReady(context.Context) error {
	n.lost.Store(true) // Route publication can change after a successful probe.
	return nil
}
func (n *foundationLostRoute) RouteHashSlot(hash uint16) (clusterpkg.Route, error) {
	if n.lost.Load() {
		return clusterpkg.Route{}, clusterpkg.ErrRouteNotReady
	}
	return n.generationWiringCluster.RouteHashSlot(hash)
}

func TestPresenceFoundationMissingRouteRemainsObservable(t *testing.T) {
	node := &foundationLostRoute{foundationFaultCluster: newFoundationFaultNode(1)}
	logger := &recordingAppLogger{}
	entry := &foundationEntry{node: node.generationWiringCluster, t: t, allowIncomplete: true}
	a, err := newTestApp(t, Config{}, WithCluster(node), WithAPI(entry), WithGateway(nil), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer func() {
		if err := a.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if entry.starts.Load() != 1 || node.claims.Load() != 0 {
		t.Fatal("missing route was retried or turned into a global readiness gate")
	}
	for _, entry := range logger.entriesSnapshot() {
		fields := make(map[string]any, len(entry.fields))
		for _, field := range entry.fields {
			fields[field.Key] = field.Value
		}
		if fields["event"] == "internal.app.presence_initial_preparation" && fields["unknown_routes"] == 1 && fields["prepared"] == 0 {
			return
		}
	}
	t.Fatal("missing route disappeared into a misleading zero-target preparation report")
}

func (n *foundationManyRoutes) RouteHashSlot(hash uint16) (clusterpkg.Route, error) {
	route := n.route
	route.HashSlot = hash
	return route, nil
}

func TestPresenceFoundationSharesForegroundLimitsAndJoinsCanceledOwnership(t *testing.T) {
	node := &foundationManyRoutes{newFoundationFaultNode(1)}
	node.snapshot = readyFakeClusterSnapshot(1, 256)
	entered, release := make(chan struct{}, 32), make(chan struct{})
	var calls, active, peak atomic.Int32
	var once sync.Once
	node.ensure = func(context.Context) error {
		calls.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		entered <- struct{}{}
		<-release // Deliberately ignore cancellation to retain actual ownership.
		return controller.ErrPresenceGenerationNotReady
	}
	entry := &foundationEntry{node: node.generationWiringCluster, t: t}
	a, err := newTestApp(t, Config{Cluster: clusterpkg.Config{NodeID: 1}}, WithCluster(node), WithAPI(entry), WithGateway(nil))
	if err != nil {
		t.Fatal(err)
	}
	recovery := a.presenceRecovery.(*presenceFullRecovery).callers
	defer func() {
		once.Do(func() { close(release) })
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := a.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	startup, cancelStartup := context.WithCancel(context.Background())
	defer cancelStartup()
	startDone := make(chan error, 1)
	go func() { startDone <- a.Start(startup) }()
	for range 16 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("initial control turns did not reach the fixed execution ceiling")
		}
	}
	foreground, cancelForeground := context.WithCancel(context.Background())
	defer cancelForeground()
	var readers sync.WaitGroup
	for hash := uint16(200); hash < 216; hash++ {
		readers.Add(1)
		go func(hash uint16) {
			defer readers.Done()
			route, _ := node.RouteHashSlot(hash)
			_, _ = recovery.Endpoints(foreground, presence.RouteTarget{HashSlot: hash, SlotID: route.SlotID, LeaderNodeID: route.Leader, LeaderTerm: route.LeaderTerm, ConfigEpoch: route.ConfigEpoch, RouteRevision: route.Revision, AuthorityEpoch: route.AuthorityEpoch}, []string{fmt.Sprintf("foreground-%d", hash)})
		}(hash)
	}
	deadline := time.Now().Add(3 * time.Second)
	for recovery.Stats().Members < 32 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	before := recovery.Stats()
	if before.Members != 32 || before.ActiveBatches != 16 || before.QueuedCalls != 16 || peak.Load() != 16 {
		t.Fatalf("preparation bypassed foreground admission: stats=%+v provider_peak=%d", before, peak.Load())
	}
	cancelForeground()
	readers.Wait()
	cancelStartup()
	select {
	case err := <-startDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled startup did not detach its callers")
	}
	retained := recovery.Stats()
	if retained.Members < 16 || retained.ActiveBatches != 16 || retained.OwnedUIDBytes == 0 || node.stops.Load() != 0 {
		t.Fatalf("credits/dependencies returned before actual provider join: %+v stops=%d", retained, node.stops.Load())
	}
	once.Do(func() { close(release) })
	join, cancelJoin := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelJoin()
	if err := a.Stop(join); err != nil {
		t.Fatal(err)
	}
	final := recovery.Stats()
	if final.Members != 0 || final.ActiveBatches != 0 || final.QueuedCalls != 0 || final.FencedUIDs != 0 || final.OwnedUIDBytes != 0 || final.OwnedResultRows != 0 || final.OwnedResultBytes != 0 || calls.Load() != 16 || active.Load() != 0 || entry.starts.Load() != 0 {
		t.Fatalf("canceled control tasks leaked or unstarted targets executed: %+v calls=%d active=%d entries=%d", final, calls.Load(), active.Load(), entry.starts.Load())
	}
}

func TestPresenceFoundationPrecedesBusinessAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"nested_identity", Config{Cluster: clusterpkg.Config{NodeID: 1}}},
		{"top_level_identity", Config{NodeID: 1}},
		{"default_identity", Config{}},
	} {
		t.Run(tc.name, func(t *testing.T) { testPresenceFoundationBeforeEntry(t, tc.cfg) })
	}
}

func testPresenceFoundationBeforeEntry(t *testing.T, cfg Config) {
	t.Helper()
	base := newFakePresenceCluster(1, nil)
	base.snapshot = readyFakeClusterSnapshot(1, 1)
	node := &generationWiringCluster{
		recoveryWiringCluster: &recoveryWiringCluster{base},
		route:                 clusterpkg.Route{HashSlot: 0, SlotID: 11, Leader: 1, LeaderTerm: 23, ConfigEpoch: 29, Revision: 17, AuthorityEpoch: 19},
	}
	entry := &foundationEntry{node: node, t: t}
	a, err := newTestApp(t, cfg, WithCluster(node), WithAPI(entry), WithGateway(nil))
	if err != nil {
		t.Fatal(err)
	}
	entry.directory = a.presenceDirectory
	entry.target = presence.RouteTarget{HashSlot: 0, SlotID: 11, LeaderNodeID: 1, LeaderTerm: 23, ConfigEpoch: 29, RouteRevision: 17, AuthorityEpoch: 19}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		if err := a.Stop(closeCtx); err != nil {
			t.Error(err)
		}
	})
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Startup must install the complete all-owner snapshot without a synthetic
	// UID or a first login. A committed claim alone is insufficient evidence.
	target := node.route
	_, complete, err := a.presenceDirectory.CurrentGeneration(presence.RouteTarget{HashSlot: target.HashSlot, SlotID: target.SlotID, LeaderNodeID: target.Leader, LeaderTerm: target.LeaderTerm, ConfigEpoch: target.ConfigEpoch, RouteRevision: target.Revision, AuthorityEpoch: target.AuthorityEpoch})
	if err != nil || !complete {
		t.Fatalf("startup did not install complete owner proof: complete=%v err=%v", complete, err)
	}
}
