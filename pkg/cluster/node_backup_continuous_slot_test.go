package cluster

import (
	"context"
	"testing"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestBackupMetadataLogPageBinaryRoundTrip(t *testing.T) {
	page := BackupMetadataLogPage{
		Records:   [][]byte{[]byte("metadata-1"), []byte("metadata-2")},
		NextIndex: 19,
		Done:      true,
	}
	body, err := marshalBackupMetadataLogPage(page)
	if err != nil {
		t.Fatalf("marshalBackupMetadataLogPage() error = %v", err)
	}
	loaded, err := loadBackupMetadataLogPage(body)
	if err != nil {
		t.Fatalf("loadBackupMetadataLogPage() error = %v", err)
	}
	if loaded.NextIndex != page.NextIndex || loaded.Done != page.Done ||
		len(loaded.Records) != len(page.Records) {
		t.Fatalf("loaded page = %#v, want %#v", loaded, page)
	}
	for index := range page.Records {
		if string(loaded.Records[index]) != string(page.Records[index]) {
			t.Fatalf("record[%d] = %q, want %q", index, loaded.Records[index], page.Records[index])
		}
	}
	body = append(body, 0)
	if _, err := loadBackupMetadataLogPage(body); err == nil {
		t.Fatal("loadBackupMetadataLogPage(trailing byte) error = nil")
	}
}

func TestContinuousBackupSlotSourceRoutesThroughRemoteLeader(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	waitAllHashSlotLeadersConverged(t, nodes)

	id := channelruntime.ChannelID{ID: "continuous-backup-remote-slot", Type: 2}
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	queryNode := firstNonLeaderNode(t, nodes, route.Leader)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	if err := nodes[0].UpsertChannelMetadata(ctx, metadb.Channel{
		ChannelID: id.ID, ChannelType: int64(id.Type), AllowStranger: 1,
	}); err != nil {
		t.Fatalf("UpsertChannelMetadata() error = %v", err)
	}
	if _, err := nodes[0].AppendChannel(ctx, channelruntime.AppendRequest{
		ChannelID: id,
		Message: channelruntime.Message{
			MessageID: 901, FromUID: "u1", ClientMsgNo: "remote-backup-1",
			ServerTimestampMS: 1_753_400_100_000, Payload: []byte("remote"),
		},
	}); err != nil {
		t.Fatalf("AppendChannel() error = %v", err)
	}

	var watermark BackupMetadataHighWatermark
	waitUntil(t, func() bool {
		var err error
		watermark, err = queryNode.ObserveBackupMetadataHighWatermark(ctx, route.HashSlot)
		return err == nil && watermark.RaftIndex > 0
	})
	if watermark.HashSlot != route.HashSlot || watermark.SlotID != route.SlotID {
		t.Fatalf("watermark = %#v, want route %#v", watermark, route)
	}
	page, err := queryNode.ReadBackupMetadataLogPage(ctx, BackupMetadataLogPageRequest{
		HashSlot: route.HashSlot, ThroughIndex: watermark.RaftIndex,
		TargetBytes: 64 << 10, MaxBytes: 1 << 20, MaxRecords: 1024,
	})
	if err != nil {
		t.Fatalf("ReadBackupMetadataLogPage(remote) error = %v", err)
	}
	if !page.Done || page.NextIndex != watermark.RaftIndex || len(page.Records) == 0 {
		t.Fatalf("metadata page = %#v, want complete non-empty remote cut", page)
	}
	foundMetadata := false
	for _, body := range page.Records {
		record, err := backupartifact.LoadMetadataLogRecord(body)
		if err != nil {
			t.Fatalf("LoadMetadataLogRecord() error = %v", err)
		}
		if record.HashSlot == route.HashSlot {
			foundMetadata = true
		}
	}
	if !foundMetadata {
		t.Fatal("remote metadata page did not contain the requested Hash Slot")
	}

	metas, _, _, err := queryNode.ListBackupChannelRuntimeMetaPage(
		ctx, route.HashSlot, metadb.ChannelRuntimeMetaCursor{}, 128,
	)
	if err != nil {
		t.Fatalf("ListBackupChannelRuntimeMetaPage(remote) error = %v", err)
	}
	for _, meta := range metas {
		if meta.ChannelID == id.ID && meta.ChannelType == int64(id.Type) {
			messageQueryNode := firstNonLeaderNode(t, nodes, meta.Leader)
			boundaries, err := messageQueryNode.ObserveBackupMessageChannels(ctx, []BackupMessageChannelRequest{{
				HashSlot: route.HashSlot, ChannelID: id.ID, ChannelType: id.Type,
				LeaderNodeID: meta.Leader, ChannelEpoch: meta.ChannelEpoch,
				LeaderEpoch: meta.LeaderEpoch, MinISR: int(meta.MinISR),
				RetentionSeq: meta.RetentionThroughSeq,
			}})
			if err != nil {
				t.Fatalf("ObserveBackupMessageChannels(remote) error = %v", err)
			}
			if len(boundaries) != 1 || boundaries[0].HW != 1 {
				t.Fatalf("remote message boundaries = %#v, want committed HW 1", boundaries)
			}
			return
		}
	}
	t.Fatalf("remote Channel metadata page = %#v, want %s", metas, id.ID)
}
