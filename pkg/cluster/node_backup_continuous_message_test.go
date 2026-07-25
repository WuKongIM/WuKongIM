package cluster

import (
	"context"
	"testing"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestNodeReadsRealCommittedMessageLogPage(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	waitChannelDataNode(t, node, 1)

	id := channelruntime.ChannelID{ID: "continuous-backup-room", Type: 2}
	route := waitRouteKeyLeaderReady(t, node, id.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for index := 0; index < 3; index++ {
		if _, err := node.AppendChannel(ctx, channelruntime.AppendRequest{
			ChannelID: id,
			Message: channelruntime.Message{
				MessageID: uint64(700 + index), FromUID: "u1",
				ClientMsgNo:       "client-" + string(rune('a'+index)),
				ServerTimestampMS: 1_753_400_100_000 + int64(index),
				Payload:           []byte{byte('a' + index)},
			},
		}); err != nil {
			t.Fatalf("AppendChannel(%d) error = %v", index, err)
		}
	}
	meta, err := node.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		t.Fatalf("GetChannelRuntimeMeta() error = %v", err)
	}
	request := BackupMessageChannelRequest{
		HashSlot: route.HashSlot, ChannelID: id.ID, ChannelType: id.Type,
		LeaderNodeID: meta.Leader, ChannelEpoch: meta.ChannelEpoch,
		LeaderEpoch: meta.LeaderEpoch, MinISR: int(meta.MinISR),
	}
	boundary, err := node.ObserveBackupMessageChannel(ctx, request)
	if err != nil {
		t.Fatalf("ObserveBackupMessageChannel() error = %v", err)
	}
	if boundary.HW != 3 || boundary.Epoch != meta.ChannelEpoch {
		t.Fatalf("boundary = %#v, want HW 3 epoch %d", boundary, meta.ChannelEpoch)
	}
	page, err := node.ReadBackupMessageLogPage(ctx, BackupMessageLogPageRequest{
		Channel: request, FromSeq: 1, ThroughSeq: boundary.HW,
		TargetBytes: 64 << 10, MaxBytes: 1 << 20, MaxRecords: 16,
	})
	if err != nil {
		t.Fatalf("ReadBackupMessageLogPage() error = %v", err)
	}
	if !page.Done || page.NextSeq != 4 || len(page.Records) != 3 || page.Boundary.HW != 3 {
		t.Fatalf("page = %#v, want complete three-row cut", page)
	}
	for index, body := range page.Records {
		record, err := backupartifact.LoadMessageLogRecord(body)
		if err != nil {
			t.Fatalf("LoadMessageLogRecord(%d) error = %v", index, err)
		}
		if record.MessageSeq != uint64(index+1) || record.MessageID != uint64(700+index) ||
			record.ChannelID != id.ID || record.Epoch != meta.ChannelEpoch {
			t.Fatalf("record[%d] = %#v", index, record)
		}
	}
}

func TestBackupMessageLogPageBinaryRoundTrip(t *testing.T) {
	page := BackupMessageLogPage{
		Boundary: BackupMessageChannelBoundary{
			HashSlot: 17, ChannelID: "continuous-backup-room", ChannelType: 2,
			Epoch: 9, LogStartOffset: 3, HW: 5,
		},
		Records: [][]byte{[]byte("record-4"), []byte("record-5")},
		NextSeq: 6,
		Done:    true,
	}
	body, err := marshalBackupMessageLogPage(page)
	if err != nil {
		t.Fatalf("marshalBackupMessageLogPage() error = %v", err)
	}
	loaded, err := loadBackupMessageLogPage(body)
	if err != nil {
		t.Fatalf("loadBackupMessageLogPage() error = %v", err)
	}
	if loaded.Boundary != page.Boundary || loaded.NextSeq != page.NextSeq ||
		loaded.Done != page.Done || len(loaded.Records) != len(page.Records) {
		t.Fatalf("loaded page = %#v, want %#v", loaded, page)
	}
	for index := range page.Records {
		if string(loaded.Records[index]) != string(page.Records[index]) {
			t.Fatalf("record[%d] = %q, want %q", index, loaded.Records[index], page.Records[index])
		}
	}
	body = append(body, 0)
	if _, err := loadBackupMessageLogPage(body); err == nil {
		t.Fatal("loadBackupMessageLogPage(trailing byte) error = nil")
	}
}
