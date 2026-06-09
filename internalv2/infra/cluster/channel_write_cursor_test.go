package cluster

import (
	"context"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestChannelWriteCursorStoreLoadAndMonotonicStore(t *testing.T) {
	node := &channelWriteCursorNodeForTest{}
	store := NewChannelWriteCursorStore(node)
	channelID := channelwrite.ChannelID{ID: "room", Type: 2}

	seq, err := store.LoadPostCommitCursor(context.Background(), channelID)
	if err != nil {
		t.Fatalf("LoadPostCommitCursor(missing) error = %v", err)
	}
	if seq != 0 {
		t.Fatalf("LoadPostCommitCursor(missing) = %d, want 0", seq)
	}

	if err := store.StorePostCommitCursor(context.Background(), channelID, 9); err != nil {
		t.Fatalf("StorePostCommitCursor(9) error = %v", err)
	}
	if err := store.StorePostCommitCursor(context.Background(), channelID, 7); err != nil {
		t.Fatalf("StorePostCommitCursor(stale) error = %v", err)
	}
	if err := store.StorePostCommitCursor(context.Background(), channelID, 11); err != nil {
		t.Fatalf("StorePostCommitCursor(11) error = %v", err)
	}

	seq, err = store.LoadPostCommitCursor(context.Background(), channelID)
	if err != nil {
		t.Fatalf("LoadPostCommitCursor() error = %v", err)
	}
	if seq != 11 {
		t.Fatalf("LoadPostCommitCursor() = %d, want 11", seq)
	}
	if got := node.stores; !reflect.DeepEqual(got, []uint64{9, 11}) {
		t.Fatalf("stored seqs = %#v, want monotonic writes 9,11", got)
	}
}

func TestChannelWriteCursorStoreRejectsInvalidStoredCursor(t *testing.T) {
	node := &channelWriteCursorNodeForTest{values: map[string][]byte{
		"2:room:" + ChannelWritePostCommitCursorMetadataKey: []byte("not-a-seq"),
	}}
	store := NewChannelWriteCursorStore(node)

	_, err := store.LoadPostCommitCursor(context.Background(), channelwrite.ChannelID{ID: "room", Type: 2})
	if err == nil {
		t.Fatalf("LoadPostCommitCursor() error = nil, want invalid cursor error")
	}
}

func TestChannelWriteCommittedReaderReadsForwardReplayPage(t *testing.T) {
	node := &channelWriteCommittedReadNodeForTest{
		result: channelstore.ReadCommittedResult{Messages: []channelv2.Message{
			{
				MessageID:         10,
				MessageSeq:        4,
				ChannelID:         "room",
				ChannelType:       2,
				FromUID:           "u1",
				ClientMsgNo:       "m1",
				Payload:           []byte("hello"),
				ServerTimestampMS: 1001,
			},
		}},
	}
	reader := NewChannelWriteCommittedReader(node)

	messages, err := reader.ReadCommittedFrom(context.Background(), channelwrite.ChannelID{ID: "room", Type: 2}, 4, 8)
	if err != nil {
		t.Fatalf("ReadCommittedFrom() error = %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != 10 || messages[0].MessageSeq != 4 || string(messages[0].Payload) != "hello" {
		t.Fatalf("messages = %#v, want committed replay message", messages)
	}
	if node.id != (channelv2.ChannelID{ID: "room", Type: 2}) {
		t.Fatalf("read channel id = %#v, want room/2", node.id)
	}
	if node.req.FromSeq != 4 || node.req.Limit != 8 || node.req.Reverse || node.req.MaxSeq != maxUint64() {
		t.Fatalf("read request = %#v, want forward from seq 4 limit 8", node.req)
	}
}

type channelWriteCursorNodeForTest struct {
	values map[string][]byte
	stores []uint64
	err    error
}

func (n *channelWriteCursorNodeForTest) LoadChannelAppMetadata(_ context.Context, channelID string, channelType int64, key string) ([]byte, error) {
	if n.err != nil {
		return nil, n.err
	}
	if n.values == nil {
		return nil, metadb.ErrNotFound
	}
	value, ok := n.values[cursorTestKey(channelID, channelType, key)]
	if !ok {
		return nil, metadb.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (n *channelWriteCursorNodeForTest) StoreChannelAppMetadata(_ context.Context, channelID string, channelType int64, key string, value []byte) error {
	if n.err != nil {
		return n.err
	}
	seq, err := decodePostCommitCursor(value)
	if err != nil {
		return err
	}
	if n.values == nil {
		n.values = make(map[string][]byte)
	}
	n.values[cursorTestKey(channelID, channelType, key)] = append([]byte(nil), value...)
	n.stores = append(n.stores, seq)
	return nil
}

func cursorTestKey(channelID string, channelType int64, key string) string {
	return channelKeyForCursorMetadata(channelID, channelType, key)
}

type channelWriteCommittedReadNodeForTest struct {
	id     channelv2.ChannelID
	req    channelstore.ReadCommittedRequest
	result channelstore.ReadCommittedResult
	err    error
}

func (n *channelWriteCommittedReadNodeForTest) ReadChannelCommitted(_ context.Context, id channelv2.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	n.id = id
	n.req = req
	if n.err != nil {
		return channelstore.ReadCommittedResult{}, n.err
	}
	return n.result, nil
}
