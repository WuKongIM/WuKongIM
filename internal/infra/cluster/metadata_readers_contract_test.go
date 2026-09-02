package cluster

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagementMetadataReadersPreservePhysicalSlotPaginationAndPointIdentity(t *testing.T) {
	t.Parallel()

	node := &contractManagementMetadataNode{
		channels:    []metadb.Channel{{ChannelID: "g2", ChannelType: 2}},
		channelNext: metadb.ChannelCursor{ChannelID: "g2", ChannelType: 2},
		runtimeRows: []metadb.ChannelRuntimeMeta{{ChannelID: "g2", ChannelType: 2, Leader: 3}},
		runtimeNext: metadb.ChannelRuntimeMetaCursor{ChannelID: "g2", ChannelType: 2},
	}
	channelReader := NewChannelBusinessReader(node)
	runtimeReader := NewChannelRuntimeMetaReader(node)
	pointReader := NewChannelRuntimeMetaPointReader(node)

	channelAfter := metadb.ChannelCursor{ChannelID: "g1", ChannelType: 2}
	channels, channelNext, channelDone, err := channelReader.ScanChannelsSlotPage(context.Background(), 17, channelAfter, 25)
	if err != nil || channelDone || len(channels) != 1 || channels[0].ChannelID != "g2" || channelNext != node.channelNext {
		t.Fatalf("channel page = %#v next=%#v done=%v err=%v", channels, channelNext, channelDone, err)
	}
	if node.channelSlot != 17 || node.channelAfter != channelAfter || node.channelLimit != 25 {
		t.Fatalf("channel scan args = slot:%d after:%#v limit:%d", node.channelSlot, node.channelAfter, node.channelLimit)
	}

	runtimeAfter := metadb.ChannelRuntimeMetaCursor{ChannelID: "g1", ChannelType: 2}
	runtimeRows, runtimeNext, runtimeDone, err := runtimeReader.ScanChannelRuntimeMetaSlotPage(context.Background(), 17, runtimeAfter, 30)
	if err != nil || runtimeDone || len(runtimeRows) != 1 || runtimeRows[0].Leader != 3 || runtimeNext != node.runtimeNext {
		t.Fatalf("runtime page = %#v next=%#v done=%v err=%v", runtimeRows, runtimeNext, runtimeDone, err)
	}
	if node.runtimeSlot != 17 || node.runtimeAfter != runtimeAfter || node.runtimeLimit != 30 {
		t.Fatalf("runtime scan args = slot:%d after:%#v limit:%d", node.runtimeSlot, node.runtimeAfter, node.runtimeLimit)
	}

	row, err := pointReader.GetChannelRuntimeMeta(context.Background(), "g2", 2)
	if err != nil || row.ChannelID != "g2" || row.ChannelType != 2 || node.pointChannelID != "g2" || node.pointChannelType != 2 {
		t.Fatalf("runtime point = %#v args=%q/%d err=%v", row, node.pointChannelID, node.pointChannelType, err)
	}
}

func TestManagementMetadataReadersFailClosedOrCompleteWhenUnwired(t *testing.T) {
	t.Parallel()

	channelAfter := metadb.ChannelCursor{ChannelID: "g1", ChannelType: 2}
	var channelReader *ChannelBusinessReader
	rows, next, done, err := channelReader.ScanChannelsSlotPage(context.Background(), 1, channelAfter, 10)
	if err != nil || !done || len(rows) != 0 || next != channelAfter {
		t.Fatalf("nil channel reader = %#v next=%#v done=%v err=%v", rows, next, done, err)
	}

	var runtimeReader *ChannelRuntimeMetaReader
	runtimeAfter := metadb.ChannelRuntimeMetaCursor{ChannelID: "g1", ChannelType: 2}
	runtimeRows, runtimeNext, runtimeDone, err := runtimeReader.ScanChannelRuntimeMetaSlotPage(context.Background(), 1, runtimeAfter, 10)
	if err != nil || !runtimeDone || len(runtimeRows) != 0 || runtimeNext != runtimeAfter {
		t.Fatalf("nil runtime reader = %#v next=%#v done=%v err=%v", runtimeRows, runtimeNext, runtimeDone, err)
	}

	var pointReader *ChannelRuntimeMetaPointReader
	if _, err := pointReader.GetChannelRuntimeMeta(context.Background(), "g1", 2); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("nil runtime point error = %v, want %v", err, metadb.ErrNotFound)
	}
}

type contractManagementMetadataNode struct {
	channels         []metadb.Channel
	channelNext      metadb.ChannelCursor
	channelSlot      uint32
	channelAfter     metadb.ChannelCursor
	channelLimit     int
	runtimeRows      []metadb.ChannelRuntimeMeta
	runtimeNext      metadb.ChannelRuntimeMetaCursor
	runtimeSlot      uint32
	runtimeAfter     metadb.ChannelRuntimeMetaCursor
	runtimeLimit     int
	pointChannelID   string
	pointChannelType int64
}

func (n *contractManagementMetadataNode) ScanChannelsSlotPage(_ context.Context, slotID uint32, after metadb.ChannelCursor, limit int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	n.channelSlot, n.channelAfter, n.channelLimit = slotID, after, limit
	return append([]metadb.Channel(nil), n.channels...), n.channelNext, false, nil
}

func (n *contractManagementMetadataNode) ScanChannelRuntimeMetaSlotPage(_ context.Context, slotID uint32, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error) {
	n.runtimeSlot, n.runtimeAfter, n.runtimeLimit = slotID, after, limit
	return append([]metadb.ChannelRuntimeMeta(nil), n.runtimeRows...), n.runtimeNext, false, nil
}

func (n *contractManagementMetadataNode) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	n.pointChannelID, n.pointChannelType = channelID, channelType
	return metadb.ChannelRuntimeMeta{ChannelID: channelID, ChannelType: channelType, Leader: 3}, nil
}
