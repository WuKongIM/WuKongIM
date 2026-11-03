package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
)

// publishProbeUnrelatedRevision follows the ordinary snapshot publication path
// without changing any Slot mapping, replica, leader, term or membership.
func publishProbeUnrelatedRevision(n *Node) error {
	n.mu.RLock()
	snapshot := n.controlSnapshot.Clone()
	n.mu.RUnlock()
	snapshot.Revision++
	return n.applySnapshot(context.Background(), snapshot)
}

func initializeProbePublication(n *Node) {
	n.controlSnapshot.Nodes[0].Health = control.NodeHealth{
		Freshness: control.NodeHealthFresh, Status: control.NodeAlive, RuntimeReady: true,
	}
	n.publishRouteAuthorityChanges(nil)
}

func TestNodeProbeWriteReadyUnrelatedRevisionDuringStatus(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 2, LeaderTerm: 10}})
	initializeProbePublication(n)
	caller := &recordingWriteProbeStatusCaller{statusesByNode: map[uint64][]routing.SlotStatus{
		2: {{SlotID: 1, Leader: 2, LeaderTerm: 10}},
	}}
	var publication error
	caller.onCall = func(uint64, []uint32) { publication = publishProbeUnrelatedRevision(n) }
	n.slotStatusCaller = caller
	err := n.ProbeWriteReady(context.Background())
	if publication != nil {
		t.Fatal(publication)
	}
	if err != nil {
		t.Fatalf("unchanged write authority rejected during logical revision publication: %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("noop proposals = %d, want exactly one", p.calls)
	}
}

func TestNodeProbeWriteReadyUnrelatedRevisionDuringNoop(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})
	initializeProbePublication(n)
	var publication error
	n.proposer = writeProbeProposerFunc(func(context.Context, propose.Request) error {
		publication = publishProbeUnrelatedRevision(n)
		return nil
	})
	err := n.ProbeWriteReady(context.Background())
	if publication != nil {
		t.Fatal(publication)
	}
	if err != nil {
		t.Fatalf("successful noop rejected after unrelated logical revision: %v", err)
	}
}

func TestNodeProbeWriteReadyUnrelatedRevisionKeepsOriginalProofExpiry(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})
	initializeProbePublication(n)
	start := time.Unix(100, 0)
	now := start
	n.writeProbe.now = func() time.Time { return now }
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = start.Add(writeProbeProofTTL / 2)
	if err := publishProbeUnrelatedRevision(n); err != nil {
		t.Fatal(err)
	}
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("unrelated revision submitted %d noops, want original proof reused", p.calls)
	}
	now = start.Add(writeProbeProofTTL)
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("unrelated revision slid proof expiry: noop calls = %d, want 2", p.calls)
	}
}

func publishProbeLeaderGap(n *Node) error {
	for _, leader := range []uint64{2, 1} {
		if err := n.updateRouteAuthorityTable(func() error {
			n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: leader, LeaderTerm: 10}})
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func TestNodeProbeWriteReadyObservedLeaderGapRejectsInflightProof(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})
	initializeProbePublication(n)
	n.proposer = writeProbeProposerFunc(func(context.Context, propose.Request) error {
		return publishProbeLeaderGap(n)
	})
	if err := n.ProbeWriteReady(context.Background()); err == nil {
		t.Fatal("observed leader gap and recovery accepted the old in-flight proof")
	}
}

func TestNodeProbeWriteReadyObservedLeaderGapRevokesCachedProof(t *testing.T) {
	p := &statusRecordingProposer{}
	n := writeProbeNodeForTest(t, p, []routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 10}})
	initializeProbePublication(n)
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := publishProbeLeaderGap(n); err != nil {
		t.Fatal(err)
	}
	if err := n.ProbeWriteReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("observed leader gap reused old proof: noop calls = %d, want 2", p.calls)
	}
}
