//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestNodeProbeWriteReadySingleNodeClusterReusesCommittedNoop(t *testing.T) {
	snapshot := nodeControlSnapshot()
	snapshot.Nodes = snapshot.Nodes[:1]
	snapshot.Slots[0].DesiredPeers = []uint64{1}
	snapshot.HashSlots.Count = 256
	snapshot.HashSlots.Ranges[0].To = 255
	cfg := validNodeConfig(t)
	cfg.Slots.HashSlotCount = 256
	cfg.Slots.TickInterval = time.Millisecond
	cfg.Slots.ElectionTick = 10
	cfg.Slots.HeartbeatTick = 1
	node, err := New(cfg, withController(control.NewStaticController(snapshot)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	waitUntil(t, func() bool {
		route, err := node.RouteHashSlot(0)
		return err == nil && route.Leader == 1 && node.Snapshot().SlotsReady
	})
	// The static Controller has no health-report writer; provide its healthy
	// placement view while exercising the real Slot runtime, proposer and disk log.
	node.channelDataNodes.UpdateAtRevision(snapshot.Revision, []uint64{1})
	before, err := node.LocalSlotLogEntries(ctx, 1, LogEntriesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := node.ProbeWriteReady(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := node.LocalSlotLogEntries(ctx, 1, LogEntriesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if after.LastIndex != before.LastIndex+1 || after.CommitIndex != after.LastIndex || after.AppliedIndex != after.LastIndex {
		t.Fatalf("20 probes should append and apply exactly one entry: before=%+v after=%+v", before, after)
	}
	if len(after.Items) == 0 || after.Items[0].DecodedType != "noop" || after.Items[0].DataSize != 12 {
		t.Fatalf("latest entry must be a 12-byte noop: %+v", after.Items)
	}
}
