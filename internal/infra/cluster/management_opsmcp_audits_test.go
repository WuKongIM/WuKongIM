package cluster

import (
	"context"
	"errors"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

type opsMCPAuditReaderStub struct {
	entries []opscontract.AuditEntry
}

func (s opsMCPAuditReaderStub) RecentAudits(context.Context, int) ([]opscontract.AuditEntry, error) {
	return append([]opscontract.AuditEntry(nil), s.entries...), nil
}

type opsMCPAuditNodeStub struct {
	snapshot control.Snapshot
	handlers map[uint64]*accessnode.OpsMCPRPCAdapter
	block    <-chan struct{}
}

func (*opsMCPAuditNodeStub) NodeID() uint64 { return 1 }

func (s *opsMCPAuditNodeStub) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return s.snapshot.Clone(), nil
}

func (s *opsMCPAuditNodeStub) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	if nodeID == 3 && s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if serviceID != accessnode.OpsMCPRPCServiceID || s.handlers[nodeID] == nil {
		return nil, errors.New("unavailable")
	}
	return s.handlers[nodeID].HandleRPC(ctx, payload)
}

func TestManagementOpsMCPAuditReaderReturnsHealthyEvidenceWhenPeerHangs(t *testing.T) {
	block := make(chan struct{})
	node := &opsMCPAuditNodeStub{
		snapshot: control.Snapshot{Nodes: []control.Node{
			{NodeID: 1, Status: control.NodeAlive},
			{NodeID: 3, Status: control.NodeSuspect},
		}},
		handlers: map[uint64]*accessnode.OpsMCPRPCAdapter{},
		block:    block,
	}
	local := opsMCPAuditReaderStub{entries: []opscontract.AuditEntry{{RequestID: "local", StartedAt: time.Now()}}}
	reader := NewManagementOpsMCPAuditReader(node, local)
	reader.timeout = 20 * time.Millisecond
	started := time.Now()
	entries, err := reader.RecentAudits(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentAudits() error = %v", err)
	}
	if time.Since(started) > time.Second || len(entries) != 1 || entries[0].RequestID != "local" {
		t.Fatalf("elapsed=%v entries=%#v", time.Since(started), entries)
	}
}

func TestOpsMCPAuditFanoutWorkerAndMergeBounds(t *testing.T) {
	if got := opsMCPAuditWorkerCount(100); got != opsMCPAuditFanoutConcurrency {
		t.Fatalf("worker count = %d, want %d", got, opsMCPAuditFanoutConcurrency)
	}
	now := time.Now()
	merged := mergeOpsMCPAuditEntries([]opscontract.AuditEntry{
		{RequestID: "older", StartedAt: now.Add(-time.Second)},
	}, []opscontract.AuditEntry{
		{RequestID: "newest", StartedAt: now},
		{RequestID: "oldest", StartedAt: now.Add(-2 * time.Second)},
	}, 2)
	if len(merged) != 2 || merged[0].RequestID != "newest" || merged[1].RequestID != "older" {
		t.Fatalf("bounded merge = %#v", merged)
	}
}

func TestManagementOpsMCPAuditReaderAggregatesNewestAcrossNodes(t *testing.T) {
	now := time.Date(2026, 7, 24, 3, 0, 0, 0, time.UTC)
	local := opsMCPAuditReaderStub{entries: []opscontract.AuditEntry{{
		RequestID: "local", StartedAt: now.Add(-time.Second),
	}}}
	remote := opsMCPAuditReaderStub{entries: []opscontract.AuditEntry{{
		RequestID: "remote", StartedAt: now,
	}}}
	node := &opsMCPAuditNodeStub{
		snapshot: control.Snapshot{Nodes: []control.Node{
			{NodeID: 1, Status: control.NodeAlive},
			{NodeID: 2, Status: control.NodeAlive},
			{NodeID: 3, Status: control.NodeDown},
		}},
		handlers: map[uint64]*accessnode.OpsMCPRPCAdapter{
			2: accessnode.NewOpsMCPRPCAdapter(nil, nil, remote),
		},
	}
	entries, err := NewManagementOpsMCPAuditReader(node, local).RecentAudits(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentAudits() error = %v", err)
	}
	if len(entries) != 2 || entries[0].RequestID != "remote" || entries[0].RecorderNodeID != 2 ||
		entries[1].RequestID != "local" || entries[1].RecorderNodeID != 1 {
		t.Fatalf("entries = %#v", entries)
	}
}
