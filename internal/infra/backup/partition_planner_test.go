package backup_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"strings"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestPartitionPlannerRequiresUnchangedAppliedIndex(t *testing.T) {
	node := &fakePartitionPlanNode{
		snapshots: []multiraft.CapturedHashSlotSnapshot{
			{SlotID: 3, HashSlot: 4, AppliedIndex: 10, CommitIndex: 10, Term: 2, CapturedAtUnixMillis: 1710000000000, Reader: backupMetadataReader(4, 0)},
			{SlotID: 3, HashSlot: 4, AppliedIndex: 11, CommitIndex: 11, Term: 2, CapturedAtUnixMillis: 1710000001000, Reader: io.NopCloser(strings.NewReader("newer"))},
		},
	}
	planner, err := backupinfra.NewPartitionPlanner(backupinfra.PartitionPlannerOptions{Node: node})
	require.NoError(t, err)
	_, err = planner.OpenPlan(context.Background(), runtimebackup.CaptureRequest{HashSlot: 4})
	require.ErrorIs(t, err, runtimebackup.ErrStaleCapture)
}

func TestPartitionPlannerRejectsCommitApplyLagWithoutPublishingCurrentWatermark(t *testing.T) {
	node := &fakePartitionPlanNode{snapshots: []multiraft.CapturedHashSlotSnapshot{{
		SlotID: 3, HashSlot: 4, AppliedIndex: 10, CommitIndex: 11, Term: 2,
		CapturedAtUnixMillis: 1710000000000, Reader: io.NopCloser(strings.NewReader("metadata")),
	}}}
	planner, err := backupinfra.NewPartitionPlanner(backupinfra.PartitionPlannerOptions{Node: node})
	require.NoError(t, err)
	_, err = planner.OpenPlan(context.Background(), runtimebackup.CaptureRequest{HashSlot: 4})
	require.ErrorIs(t, err, runtimebackup.ErrStaleCapture)
}

func TestPartitionPlannerGroupsStableChannelLeaders(t *testing.T) {
	node := &fakePartitionPlanNode{
		snapshots: []multiraft.CapturedHashSlotSnapshot{
			{SlotID: 3, HashSlot: 4, AppliedIndex: 10, CommitIndex: 10, Term: 2, CapturedAtUnixMillis: 1710000000000, Reader: backupMetadataReader(4, 3)},
			{SlotID: 3, HashSlot: 4, AppliedIndex: 10, CommitIndex: 10, Term: 2, CapturedAtUnixMillis: 1710000001000, Reader: io.NopCloser(strings.NewReader("same"))},
		},
		metas: []metadb.ChannelRuntimeMeta{
			{ChannelID: "a", ChannelType: 2, Leader: 2, ChannelEpoch: 1, LeaderEpoch: 3, MinISR: 2},
			{ChannelID: "b", ChannelType: 2, Leader: 1, ChannelEpoch: 2, LeaderEpoch: 4, MinISR: 1},
		},
	}
	planner, err := backupinfra.NewPartitionPlanner(backupinfra.PartitionPlannerOptions{Node: node})
	require.NoError(t, err)
	plan, err := planner.OpenPlan(context.Background(), runtimebackup.CaptureRequest{HashSlot: 4, Kind: backupartifact.RestorePointSyntheticFull})
	require.NoError(t, err)
	defer plan.Close()
	require.Equal(t, uint64(10), plan.Cut().RaftIndex)
	require.Equal(t, uint64(3), plan.MetadataRecordCount())
	require.Nil(t, plan.Base())
	shards := plan.MessageShards()
	require.Len(t, shards, 2)
	require.Equal(t, uint64(1), shards[0].NodeID)
	require.Equal(t, "b", shards[0].Channels[0].ChannelID)
	require.Equal(t, uint64(2), shards[1].NodeID)
}

func backupMetadataReader(hashSlot uint16, entryCount uint64) io.ReadCloser {
	body := make([]byte, 0, 22)
	body = append(body, 'W', 'K', 'D', 'B')
	body = binary.BigEndian.AppendUint16(body, 1)
	body = binary.BigEndian.AppendUint16(body, 1)
	body = binary.BigEndian.AppendUint16(body, hashSlot)
	body = binary.BigEndian.AppendUint64(body, entryCount)
	body = binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body))
	return io.NopCloser(bytes.NewReader(body))
}

type fakePartitionPlanNode struct {
	snapshots []multiraft.CapturedHashSlotSnapshot
	metas     []metadb.ChannelRuntimeMeta
	index     int
}

func (n *fakePartitionPlanNode) CaptureBackupHashSlotSnapshot(context.Context, uint16) (multiraft.CapturedHashSlotSnapshot, error) {
	result := n.snapshots[n.index]
	n.index++
	return result, nil
}

func (n *fakePartitionPlanNode) ListBackupChannelRuntimeMetaPage(_ context.Context, _ uint16, after metadb.ChannelRuntimeMetaCursor, _ int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	if after != (metadb.ChannelRuntimeMetaCursor{}) {
		return nil, after, true, nil
	}
	if len(n.metas) == 0 {
		return nil, after, true, nil
	}
	last := n.metas[len(n.metas)-1]
	return append([]metadb.ChannelRuntimeMeta(nil), n.metas...), metadb.ChannelRuntimeMetaCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}, true, nil
}
