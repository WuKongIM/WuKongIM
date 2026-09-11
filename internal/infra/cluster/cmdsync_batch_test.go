package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

func TestCMDBatchGroupsSourceReadsAndContinuesOnlyUnfinishedLogs(t *testing.T) {
	node := &cmdBatchNode{cmdSyncNodeFake: &cmdSyncNodeFake{}}
	store := NewCMDSyncStore(node)
	store.CommandChannelSuffix = "__custom"
	queries := []cmdsync.CommandMessageRead{
		{Key: cmdsync.CommandChannelKey{ChannelID: "empty__custom", ChannelType: 2}, FromSeq: 1, Limit: 2},
		{Key: cmdsync.CommandChannelKey{ChannelID: "live__custom", ChannelType: 2}, FromSeq: 1, Limit: 2},
		{Key: cmdsync.CommandChannelKey{ChannelID: "other__custom", ChannelType: 8}, FromSeq: 9, Limit: 1},
	}
	got, err := store.LoadCommandMessagesBatch(context.Background(), queries)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(got[0]) != 0 || len(got[1]) != 2 || len(got[2]) != 1 || got[1][1].MessageSeq != 2 || got[2][0].MessageSeq != 9 {
		t.Fatalf("lost aligned reads: %+v", got)
	}
	if node.metadataCalls != 1 || !reflect.DeepEqual(node.batchSizes, []int{3, 1}) {
		t.Fatalf("metadata=%d reads=%v", node.metadataCalls, node.batchSizes)
	}
	for i, source := range []string{"empty", "live", "other"} {
		if node.facts[i].ChannelID != source || node.facts[i].ChannelType != int64(queries[i].Key.ChannelType) || node.facts[i].Kind != slotproxy.PermissionMetadataReadChannel {
			t.Fatalf("wrong authoritative source: %+v", node.facts[i])
		}
	}
}

func TestCMDBatchFailsClosedForUnavailableOrAmbiguousResults(t *testing.T) {
	for _, mode := range []string{"metadata_shape", "read_shape", "read_error", "outer_absence"} {
		node := &cmdBatchNode{cmdSyncNodeFake: &cmdSyncNodeFake{}, mode: mode}
		q := []cmdsync.CommandMessageRead{{Key: cmdsync.CommandChannelKey{ChannelID: "a____cmd", ChannelType: 2}, Limit: 1}, {Key: cmdsync.CommandChannelKey{ChannelID: "b____cmd", ChannelType: 2}, Limit: 1}}
		if got, err := NewCMDSyncStore(node).LoadCommandMessagesBatch(context.Background(), q); err == nil || got != nil {
			t.Fatalf("%s fabricated success: %v %v", mode, got, err)
		}
	}
	node := &cmdBatchNode{cmdSyncNodeFake: &cmdSyncNodeFake{}}
	if _, err := NewCMDSyncStore(node).LoadCommandMessagesBatch(context.Background(), make([]cmdsync.CommandMessageRead, cmdsync.MaxCommandReadBatch+1)); err == nil || node.metadataCalls != 0 {
		t.Fatal("oversized batch reached storage")
	}
}

type cmdBatchNode struct {
	*cmdSyncNodeFake
	metadataCalls int
	facts         []slotproxy.PermissionMetadataRead
	batchSizes    []int
	mode          string
}

func (n *cmdBatchNode) ReadPermissionMetadataBatchAuthoritative(_ context.Context, reads []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult {
	n.metadataCalls++
	n.facts = append(n.facts, reads...)
	if n.mode == "metadata_shape" {
		return nil
	}
	return make([]slotproxy.PermissionMetadataReadResult, len(reads))
}
func (n *cmdBatchNode) ReadChannelCommittedBatch(_ context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.batchSizes = append(n.batchSizes, len(reads))
	if n.mode == "read_shape" {
		return nil, nil
	}
	if n.mode == "outer_absence" {
		return nil, channelruntime.ErrChannelNotFound
	}
	rows := make([]clusterchannels.CommittedReadResult, len(reads))
	for i, r := range reads {
		if n.mode == "read_error" {
			rows[i].Err = errors.New("leader unavailable")
			continue
		}
		if r.ChannelID.ID == "empty__custom" {
			rows[i].Err = channelruntime.ErrChannelNotFound
			continue
		}
		rows[i].Read = channelstore.ReadCommittedResult{Messages: []channelruntime.Message{{MessageID: r.Request.FromSeq, MessageSeq: r.Request.FromSeq, ChannelID: r.ChannelID.ID, ChannelType: r.ChannelID.Type}}, NextSeq: r.Request.FromSeq + 1}
	}
	return rows, nil
}
