package backup_test

import (
	"context"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

func TestMetadataLogSourceMapsAppliedRaftPagesToContinuousCapture(t *testing.T) {
	record, err := backupartifact.MarshalMetadataLogRecord(backupartifact.MetadataLogRecord{
		HashSlot: 17, RaftIndex: 42, RaftTerm: 7,
		CommittedAtUnixMillis: 1_753_400_100_000,
		Command:               []byte{1, 2, 3},
	})
	require.NoError(t, err)
	node := &fakeContinuousMetadataNode{
		watermark: clusterpkg.BackupMetadataHighWatermark{
			HashSlot: 17, SlotID: 9, RaftIndex: 42,
			ObservedAtUnixMillis: 1_753_400_100_000,
		},
		page: clusterpkg.BackupMetadataLogPage{
			Records: [][]byte{record}, NextIndex: 42, Done: true,
		},
	}
	source, err := backupinfra.NewMetadataLogSource(node)
	require.NoError(t, err)

	watermark, err := source.HighWatermark(context.Background(), 17, "slot-generation-1", backupcontract.StreamFrontier{})
	require.NoError(t, err)
	require.Equal(t, uint64(42), watermark.Position)
	page, err := source.ReadPage(context.Background(), runtimebackup.SourcePageRequest{
		HashSlot: 17, Stream: backupartifact.SegmentStreamMetadata,
		ThroughPosition: 42, MaxBytes: 64 << 10, MaxRecordBytes: 1 << 20, MaxRecords: 1024,
	})
	require.NoError(t, err)
	require.Equal(t, "42", page.NextCursor)
	require.True(t, page.Done)
	require.Equal(t, record, page.Records[0])
	require.Equal(t, uint64(42), node.request.ThroughIndex)
	require.Equal(t, int64(64<<10), node.request.TargetBytes)
	require.Equal(t, int64(1<<20), node.request.MaxBytes)
}

type fakeContinuousMetadataNode struct {
	watermark clusterpkg.BackupMetadataHighWatermark
	page      clusterpkg.BackupMetadataLogPage
	request   clusterpkg.BackupMetadataLogPageRequest
}

func (n *fakeContinuousMetadataNode) ObserveBackupMetadataHighWatermark(context.Context, uint16) (clusterpkg.BackupMetadataHighWatermark, error) {
	return n.watermark, nil
}

func (n *fakeContinuousMetadataNode) ReadBackupMetadataLogPage(_ context.Context, request clusterpkg.BackupMetadataLogPageRequest) (clusterpkg.BackupMetadataLogPage, error) {
	n.request = request
	return n.page, nil
}
