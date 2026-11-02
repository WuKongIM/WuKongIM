//go:build integration

package replication_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// Release must preserve both an admitted third-voter callback that has not
// reported its gap yet, and a repair that already owns an unfinished gap.
func TestRuntimeReleasePreservesFollowerRepair(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		pendingRepair bool
		reinstallHeld bool
	}{
		{name: "late_original_callback"},
		{name: "running_repair", pendingRepair: true},
		{name: "late_callback_same_authority_reinstall", reinstallHeld: true},
		{name: "running_repair_same_authority_reinstall", pendingRepair: true, reinstallHeld: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			name := scenario.name
			router := &releaseSafetyRouter{servers: make(map[ch.NodeID]*replication.ExchangeServer)}
			gate := &releaseSafetyGate{started: make(chan struct{}), release: make(chan struct{}), pendingRepair: scenario.pendingRepair}
			var unblock sync.Once
			open := func() { unblock.Do(func() { close(gate.release) }) }
			defer open()
			owners := make(map[ch.NodeID]*replication.Runtime)
			completedNormally := false
			defer func() {
				open()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				joined := true
				for _, owner := range owners {
					if err := owner.Close(ctx); err != nil {
						joined = false
						t.Errorf("Runtime.Close: %v", err)
					}
				}
				t.Logf(`release_safety_report={"case":%q,"cleanup_joined":%t,"passed":%t,"native_heap_cause_proved":false}`, name, joined, completedNormally && !t.Failed())
			}()
			for _, node := range []ch.NodeID{1, 2, 3} {
				store, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{
					Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 64, MaxBatchBytes: 4 << 20,
				})
				if err != nil {
					t.Fatal(err)
				}
				owner, err := replication.NewRuntime(replication.RuntimeConfig{
					LocalNode: node, Store: store, Link: releaseSafetyLink{from: node, router: router, gate: gate}, MaxChannels: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				owners[node] = owner
				router.servers[node] = owner.ExchangeServer()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			id := ch.ChannelID{ID: "release-safety-" + name, Type: 1}
			a := replication.Authority{
				Key: ch.ChannelKeyForID(id), ChannelID: id,
				ID:     replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1},
				Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
			}
			log := owners[1].Log()
			if _, err := log.Install(ctx, a); err != nil {
				t.Fatal(err)
			}
			proposal := replication.Proposal{
				Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{31: 91},
				Records: []ch.Record{{ID: 91, Epoch: 1, FromUID: "sender", ClientMsgNo: "release-91", Payload: []byte("retained durability"), SizeBytes: 19, ServerTimestampMS: 91}},
			}
			receipt, err := log.Commit(ctx, proposal)
			if err != nil || receipt.First != 1 || receipt.Last != 1 || receipt.HW != 1 {
				t.Fatalf("quorum commit = %+v, %v; want exact durable range 1", receipt, err)
			}
			select {
			case <-gate.started:
			case <-ctx.Done():
				t.Fatal("third-voter work never reached the network gate")
			}
			quorumReplica := releaseSafetyProbe(t, ctx, owners[2].ExchangeServer(), a, 2)
			if !log.Release(a.Key, a.ID) {
				t.Fatal("Release refused the quiesced exact authority")
			}
			if scenario.reinstallHeld {
				// The original generation still owns the blocked peer item here.
				// Reinstall must reactivate its authority before that item retires.
				installed, err := log.Install(ctx, a)
				if err != nil || installed.HW != receipt.HW {
					t.Fatalf("reinstall with peer debt = %+v, %v; want HW %d", installed, err, receipt.HW)
				}
			}
			open()
			// Observe recovery through the real public follower exchange endpoint,
			// never through its storage implementation or private repair ledger.
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				follower := releaseSafetyProbe(t, ctx, owners[3].ExchangeServer(), a, 3)
				if follower.State.LEO == 1 && len(follower.Entries) == 1 && follower.Entries[0].Present &&
					len(quorumReplica.Entries) == 1 && follower.Entries[0].Identity == quorumReplica.Entries[0].Identity {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("Release discarded admitted third-voter repair")
				}
			}
			if scenario.reinstallHeld {
				// The first public probe can observe storage before its completion
				// callback returns. Separate the next faulted round with a bounded
				// idle turn, then require fresh repair under the same authority.
				idle := time.NewTimer(25 * time.Millisecond)
				select {
				case <-idle.C:
				case <-ctx.Done():
					idle.Stop()
					t.Fatal(ctx.Err())
				}
				gate.rejectNext.Store(true)
				fresh := replication.Proposal{
					Key: a.Key, Expected: a.ID, CommandID: ch.CommandID{31: 92},
					Records: []ch.Record{{ID: 92, Epoch: 1, FromUID: "sender", ClientMsgNo: "release-92", Payload: []byte("fresh repair"), SizeBytes: 12, ServerTimestampMS: 92}},
				}
				freshReceipt, err := log.Commit(ctx, fresh)
				if err != nil || freshReceipt.First != 2 || freshReceipt.Last != 2 || freshReceipt.HW != 2 {
					t.Fatalf("fresh same-authority commit = %+v, %v; want durable range 2", freshReceipt, err)
				}
				for {
					follower := releaseSafetyProbe(t, ctx, owners[3].ExchangeServer(), a, 3)
					if follower.State.LEO == 2 {
						quorum := releaseSafetyProbe(t, ctx, owners[2].ExchangeServer(), a, 2)
						if follower.State.TailIdentity != quorum.State.TailIdentity {
							t.Fatal("fresh same-authority repair produced a different durable tail")
						}
						break
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("same-authority reinstall lost fresh follower-repair capability")
					}
				}
			}
			installed, err := log.Install(ctx, a)
			wantHW := receipt.HW
			if scenario.reinstallHeld {
				wantHW = 2
			}
			if err != nil || installed.HW != wantHW {
				t.Fatalf("reinstall = %+v, %v; want recovered HW %d", installed, err, wantHW)
			}
			replayed, err := log.Commit(ctx, proposal)
			if err != nil || replayed != receipt {
				t.Fatalf("exact retry after release = %+v, %v; want %+v", replayed, err, receipt)
			}
			newer := a
			newer.ID.FenceVersion++
			if _, err := log.Install(ctx, newer); err != nil {
				t.Fatal(err)
			}
			if log.Release(a.Key, a.ID) {
				t.Fatal("old Release retired a newer installed authority")
			}
			if _, err := log.Commit(ctx, proposal); !errors.Is(err, ch.ErrStaleMeta) {
				t.Fatalf("old proposal error = %v; want stale authority", err)
			}
			if !log.Release(newer.Key, newer.ID) {
				t.Fatal("new authority was damaged by the old Release")
			}
			completedNormally = true
		})
	}
}

func releaseSafetyProbe(t *testing.T, ctx context.Context, server *replication.ExchangeServer, a replication.Authority, follower ch.NodeID) replication.ProbeResult {
	t.Helper()
	request := replication.ProbeRequest{ChannelKey: a.Key, ChannelID: a.ChannelID, Leader: 1, Follower: follower, Indexes: []uint64{1}}
	result, err := server.Handle(ctx, 1, replication.ExchangeBatch{
		Version: replication.ExchangeVersion, Priority: replication.ExchangePriorityForeground,
		Items: []replication.ExchangeItem{{RequestID: 1, Kind: replication.ExchangeProbe, Probe: &request}},
	})
	if err != nil || len(result.Items) != 1 || result.Items[0].RequestID != 1 {
		t.Fatalf("public follower probe = %+v, %v", result, err)
	}
	return result.Items[0].Probe
}

type releaseSafetyRouter struct {
	servers map[ch.NodeID]*replication.ExchangeServer
}

type releaseSafetyGate struct {
	started       chan struct{}
	release       chan struct{}
	pendingRepair bool
	calls         atomic.Int32
	rejectNext    atomic.Bool
}

// This fault belongs only to the external network adapter. The runtime,
// sequencer, local storage adapter and each follower endpoint remain real.
type releaseSafetyLink struct {
	from   ch.NodeID
	router *releaseSafetyRouter
	gate   *releaseSafetyGate
}

func (l releaseSafetyLink) Exchange(ctx context.Context, node ch.NodeID, batch replication.ExchangeBatch) (replication.ExchangeBatchResult, error) {
	if l.from == 1 && node == 3 {
		for _, item := range batch.Items {
			if item.Kind != replication.ExchangeReplicate {
				continue
			}
			if l.gate.rejectNext.CompareAndSwap(true, false) {
				return replication.ExchangeBatchResult{}, fmt.Errorf("injected fresh third-voter transport failure")
			}
			call := l.gate.calls.Add(1)
			if l.gate.pendingRepair && call == 1 {
				return replication.ExchangeBatchResult{}, fmt.Errorf("injected first third-voter transport failure")
			}
			if call == 1 || l.gate.pendingRepair && call == 2 {
				close(l.gate.started)
				select {
				case <-l.gate.release:
				case <-ctx.Done():
					return replication.ExchangeBatchResult{}, ctx.Err()
				}
				if !l.gate.pendingRepair {
					return replication.ExchangeBatchResult{}, fmt.Errorf("injected late third-voter transport failure")
				}
			}
		}
	}
	return l.router.servers[node].Handle(ctx, l.from, batch)
}
