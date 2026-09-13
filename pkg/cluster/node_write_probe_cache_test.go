package cluster

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestNodeProbeWriteReadyReusesSuccessfulProof(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
	for range 20 {
		if err := node.ProbeWriteReady(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if proposer.calls != 1 {
		t.Fatalf("repeated readiness checks submitted %d noops, want one recent write proof", proposer.calls)
	}
}

func TestNodeProbeWriteReadyProofExpiryDoesNotSlide(t *testing.T) {
	proposer := &statusRecordingProposer{}
	node := writeProbeNodeForTest(t, proposer, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
	start := time.Unix(100, 0)
	now := start
	node.writeProbe.now = func() time.Time { return now }
	for _, offset := range []time.Duration{0, writeProbeProofTTL / 2, writeProbeProofTTL - 1} {
		now = start.Add(offset)
		if err := node.ProbeWriteReady(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if proposer.calls != 1 {
		t.Fatalf("calls before expiry = %d, want 1", proposer.calls)
	}
	now = start.Add(writeProbeProofTTL)
	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if proposer.calls != 2 {
		t.Fatalf("calls at expiry = %d, want 2", proposer.calls)
	}
	// A backwards clock cannot make a future-dated proof reusable.
	now = start
	if err := node.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if proposer.calls != 3 {
		t.Fatalf("calls after clock rollback = %d, want 3", proposer.calls)
	}
}

func TestNodeProbeWriteReadyCachedProofKeepsLiveGates(t *testing.T) {
	for _, gate := range []string{"closed_slot", "term_mismatch", "routes", "placement", "maintenance", "stopping"} {
		t.Run(gate, func(t *testing.T) {
			p := &statusRecordingProposer{}
			n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})
			if err := n.ProbeWriteReady(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch gate {
			case "closed_slot":
				p.errs = map[multiraft.SlotID]error{1: multiraft.ErrSlotClosed}
			case "term_mismatch":
				status := p.statuses[1]
				status.Term++
				p.statuses[1] = status
			case "routes":
				n.snapshot.RoutesReady = false
			case "placement":
				n.channelDataNodes.UpdateAtRevision(1, nil)
			case "maintenance":
				n.setMaintenance(true)
			case "stopping":
				n.stopping.Store(true)
			}
			if err := n.ProbeWriteReady(context.Background()); err == nil {
				t.Fatal("cached proof hid a failed live gate")
			}
			if p.calls != 1 {
				t.Fatalf("failed live gate proposed %d noops, want original 1", p.calls)
			}
			p.errs = nil
			p.statuses[1] = multiraft.Status{SlotID: 1, LeaderID: 1, Term: 10}
			n.snapshot.RoutesReady = true
			n.channelDataNodes.UpdateAtRevision(1, []uint64{1})
			n.setMaintenance(false)
			n.stopping.Store(false)
			if err := n.ProbeWriteReady(context.Background()); err != nil {
				t.Fatal(err)
			}
			if p.calls != 2 {
				t.Fatalf("recovered gate reused old proof: proposals = %d, want 2", p.calls)
			}
		})
	}
}

func TestNodeProbeWriteReadyProofFencesAuthorityChanges(t *testing.T) {
	for _, change := range []string{"term", "leader", "revision", "maintenance_cycle"} {
		t.Run(change, func(t *testing.T) {
			p := &statusRecordingProposer{}
			n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})
			if err := n.ProbeWriteReady(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "term":
				p.statuses[1] = multiraft.Status{SlotID: 1, LeaderID: 1, Term: 11}
				n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 11}})
			case "leader":
				p.statuses[1] = multiraft.Status{SlotID: 1, LeaderID: 2, Term: 11}
				n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 11}})
				n.slotStatusCaller = &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
					2: {{SlotID: 1, Leader: 2, LeaderTerm: 11}},
				}}
			case "revision":
				n.controlSnapshot.Revision++
				n.controlSnapshot.Slots[0].ConfigEpoch++
				n.snapshot.StateRevision++
				if err := n.router.UpdateControlSnapshot(n.controlSnapshot); err != nil {
					t.Fatal(err)
				}
			case "maintenance_cycle":
				n.setMaintenance(true)
				n.setMaintenance(false)
			}
			if err := n.ProbeWriteReady(context.Background()); err != nil {
				t.Fatal(err)
			}
			if p.calls != 2 {
				t.Fatalf("authority change reused old proof: proposals = %d, want 2", p.calls)
			}
		})
	}
}

func TestNodeProbeWriteReadyCoalescesConcurrentRequests(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	n.proposer = writeProbeProposerFunc(func(ctx context.Context, _ propose.Request) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	const requests = 20
	results := make(chan error, requests)
	go func() { results <- n.ProbeWriteReady(context.Background()) }()
	<-entered
	checked := make(chan struct{}, requests*2)
	n.slotStatusRuntime = slotStatusReaderFunc(func(id multiraft.SlotID) (multiraft.Status, error) {
		checked <- struct{}{}
		return p.Status(id)
	})
	for range requests - 1 {
		go func() { results <- n.ProbeWriteReady(context.Background()) }()
	}
	for range requests - 1 {
		<-checked
	}
	close(release)
	for range requests {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("%d concurrent checks proposed %d noops, want 1", requests, got)
	}
}

func TestNodeProbeWriteReadyCanceledWaiterDoesNotCancelOwner(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
	entered, release := make(chan struct{}), make(chan struct{})
	n.proposer = writeProbeProposerFunc(func(context.Context, propose.Request) error {
		close(entered)
		<-release
		return nil
	})
	owner := make(chan error, 1)
	go func() { owner <- n.ProbeWriteReady(context.Background()) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	n.slotStatusRuntime = slotStatusReaderFunc(func(id multiraft.SlotID) (multiraft.Status, error) {
		cancel()
		return p.Status(id)
	})
	if err := n.ProbeWriteReady(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter = %v", err)
	}
	close(release)
	if err := <-owner; err != nil {
		t.Fatalf("waiter cancellation poisoned owner: %v", err)
	}
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNodeProbeWriteReadyInflightMaintenanceCannotPublishProof(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
	n.proposer = writeProbeProposerFunc(func(context.Context, propose.Request) error {
		n.setMaintenance(true)
		n.setMaintenance(false)
		return nil
	})
	if err := n.ProbeWriteReady(context.Background()); !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("probe crossing maintenance = %v, want ErrRouteNotReady", err)
	}
	n.proposer = p
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatal("in-flight maintenance result was cached")
	}
}

func TestNodeProbeWriteReadyWaiterRetriesCanceledOwner(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
	entered := make(chan struct{})
	var calls atomic.Int32
	n.proposer = writeProbeProposerFunc(func(ctx context.Context, _ propose.Request) error {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner, waiter := make(chan error, 1), make(chan error, 1)
	go func() { owner <- n.ProbeWriteReady(ctx) }()
	<-entered
	checked := make(chan struct{}, 2)
	n.slotStatusRuntime = slotStatusReaderFunc(func(id multiraft.SlotID) (multiraft.Status, error) {
		checked <- struct{}{}
		return p.Status(id)
	})
	go func() { waiter <- n.ProbeWriteReady(context.Background()) }()
	<-checked
	cancel()
	if err := <-owner; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner = %v, want canceled", err)
	}
	if err := <-waiter; err != nil {
		t.Fatalf("healthy waiter inherited owner's cancellation: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("proposal calls = %d, want canceled owner plus successful retry", calls.Load())
	}
}

func TestNodeProbeWriteReadyDoesNotCacheFailedOrSlowProbe(t *testing.T) {
	for _, kind := range []string{"failure", "canceled_owner", "slow_success"} {
		t.Run(kind, func(t *testing.T) {
			p := &statusRecordingProposer{}
			n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
			now := time.Unix(100, 0)
			n.writeProbe.now = func() time.Time { return now }
			sentinel := errors.New("quorum unavailable")
			n.proposer = writeProbeProposerFunc(func(context.Context, propose.Request) error {
				switch kind {
				case "failure":
					return sentinel
				case "canceled_owner":
					return context.Canceled
				default:
					now = now.Add(writeProbeProofTTL)
					return nil
				}
			})
			err := n.ProbeWriteReady(context.Background())
			if kind == "slow_success" && err != nil || kind == "failure" && !errors.Is(err, sentinel) || kind == "canceled_owner" && !errors.Is(err, context.Canceled) {
				t.Fatalf("initial result = %v", err)
			}
			n.proposer = p
			if err := n.ProbeWriteReady(context.Background()); err != nil {
				t.Fatal(err)
			}
			if p.calls != 1 {
				t.Fatalf("%s was reused without a fresh proposal", kind)
			}
		})
	}
}

type writeProbeProposerFunc func(context.Context, propose.Request) error

func (f writeProbeProposerFunc) Propose(ctx context.Context, req propose.Request) error {
	return f(ctx, req)
}

func TestNodeProbeWriteReadyCacheDoesNotRenewDataPlaneLease(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1}})
	n.channels = noopChannelService{}
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := n.channelDataPlaneLease.lastOK.Load()
	if first == nil {
		t.Fatal("fresh probe did not publish lease evidence")
	}
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.channelDataPlaneLease.lastOK.Load() != first {
		t.Fatal("cache hit renewed lease evidence")
	}
}
