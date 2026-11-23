//go:build integration

package replication

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestQuorumCommittedRetryRetainedHeap(t *testing.T) {
	const channelCount, commandsPerChannel = 192, 64
	const retryHeapBudget = channelCount * commandsPerChannel * 320
	harness := newReplicaHarness(t, 1, 2, 3)
	log, err := newQuorumLog(quorumLogConfig{
		Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
		RecoveryTimeout: time.Second, RecoveryPageBytes: 64 << 10,
		MaxChannels: channelCount, MaxVoters: 3, MaxProposalRecords: 4,
		MaxProposalBytes: 64 << 10, MaxRetainedCommands: commandsPerChannel,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	authorityID := AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7}
	keys := make([]ch.ChannelKey, channelCount)
	for i := range keys {
		id := ch.ChannelID{ID: fmt.Sprintf("retry-memory-%04d", i), Type: 1}
		authority := Authority{Key: ch.ChannelKeyForID(id), ChannelID: id, ID: authorityID, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
		keys[i] = authority.Key
		if _, err := log.Install(ctx, authority); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
		var first Proposal
		var firstReceipt Receipt
		for j := range commandsPerChannel {
			proposal := Proposal{
				Key: authority.Key, Expected: authorityID, CommandID: ch.CommandID{31: byte(j + 1)},
				Records: []ch.Record{{
					ID: uint64(i*commandsPerChannel + j + 1), Epoch: authorityID.ChannelEpoch,
					FromUID: "sender", ClientMsgNo: fmt.Sprintf("retry-%d", j),
					Payload: []byte("payload"), SizeBytes: len("payload"), ServerTimestampMS: int64(j + 1),
				}},
			}
			receipt, err := log.Commit(ctx, proposal)
			if err != nil || receipt.First != uint64(j+1) || receipt.Last != receipt.First || receipt.HW != receipt.First {
				t.Fatalf("commit %d/%d: %+v %v", i, j, receipt, err)
			}
			if j == 0 {
				first, firstReceipt = proposal, receipt
			}
		}
		writes := harness.syncCalls
		if receipt, err := log.Commit(ctx, first); err != nil || receipt != firstReceipt || harness.syncCalls != writes {
			t.Fatalf("first retry after %d commits: %+v %v (writes %d→%d)", commandsPerChannel, receipt, err, writes, harness.syncCalls)
		}
	}

	heap := func() uint64 {
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		runtime.KeepAlive(keys)
		runtime.KeepAlive(log)
		runtime.KeepAlive(harness)
		return stats.HeapAlloc
	}
	resident := heap()
	for i, key := range keys {
		if !log.Release(key, authorityID) {
			t.Fatalf("release %d refused the exact quiesced authority", i)
		}
	}
	// Durable backing stores stay live in both samples. Release changes only
	// process-local Channel state, including the exact retry cache.
	retained := int64(resident) - int64(heap())
	t.Logf("channels=%d retained_commands=%d released_heap_bytes=%d budget_bytes=%d", channelCount, channelCount*commandsPerChannel, retained, retryHeapBudget)
	if retained > retryHeapBudget {
		t.Fatalf("resident retry state held %d bytes, budget %d", retained, retryHeapBudget)
	}
}
