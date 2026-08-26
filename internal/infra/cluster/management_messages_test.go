package cluster

import (
	"context"
	"errors"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestManagementMessageReaderReadsCommittedMessagesDescending(t *testing.T) {
	node := &recordingManagementMessageNode{
		result: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{
			{MessageID: 101, MessageSeq: 10, ClientMsgNo: "c-101", ChannelID: "room-1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 1713859200123, Payload: []byte("hello")},
			{MessageID: 100, MessageSeq: 9, ClientMsgNo: "c-100", ChannelID: "room-1", ChannelType: 2, FromUID: "u2", ServerTimestampMS: 1713859100000, Payload: []byte("older")},
		}},
	}
	reader := NewManagementMessageReader(node)

	got, err := reader.QueryMessages(context.Background(), managementusecase.MessageQueryRequest{
		ChannelID: "room-1", ChannelType: 2, BeforeSeq: 12, Limit: 1,
	})

	if err != nil {
		t.Fatalf("QueryMessages() error = %v", err)
	}
	if node.channelID != (channelruntime.ChannelID{ID: "room-1", Type: 2}) {
		t.Fatalf("channel id = %#v, want room-1:2", node.channelID)
	}
	if node.req.FromSeq != 11 || node.req.Limit != 2 || !node.req.Reverse {
		t.Fatalf("read request = %#v, want before 12 as reverse from seq 11 with limit+1", node.req)
	}
	if !got.HasMore || got.NextBeforeSeq != 10 {
		t.Fatalf("page = %#v, want has_more with next before seq 10", got)
	}
	want := []managementusecase.Message{{MessageID: 101, MessageSeq: 10, ClientMsgNo: "c-101", ChannelID: "room-1", ChannelType: 2, FromUID: "u1", Timestamp: 1713859200, Payload: []byte("hello")}}
	if !sameManagementMessages(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
}

func TestMergeLatestMessagePagesOrdersAndDeduplicatesReplicas(t *testing.T) {
	message105 := managementusecase.Message{MessageID: 105, MessageSeq: 5, ChannelID: "a", ChannelType: 2, Payload: []byte("same")}
	page, err := mergeLatestMessagePages([]latestMessageNodePage{
		{nodeID: 2, items: []managementusecase.Message{message105, {MessageID: 103, MessageSeq: 3, ChannelID: "c", ChannelType: 2}}},
		{nodeID: 1, items: []managementusecase.Message{{MessageID: 104, MessageSeq: 4, ChannelID: "b", ChannelType: 2}, message105}},
	}, 2)
	if err != nil {
		t.Fatalf("mergeLatestMessagePages(): %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].MessageID != 105 || page.Items[1].MessageID != 104 || !page.HasMore || page.NextBeforeMessageID != 104 {
		t.Fatalf("page = %#v, want 105,104 and more", page)
	}
}

func TestMergeLatestMessagePagesRejectsReplicaMismatch(t *testing.T) {
	_, err := mergeLatestMessagePages([]latestMessageNodePage{
		{nodeID: 1, items: []managementusecase.Message{{MessageID: 105, MessageSeq: 5, ChannelID: "a", ChannelType: 2}}},
		{nodeID: 2, items: []managementusecase.Message{{MessageID: 105, MessageSeq: 6, ChannelID: "a", ChannelType: 2}}},
	}, 2)
	if err == nil {
		t.Fatal("mergeLatestMessagePages() error = nil, want replica mismatch")
	}
}

func TestManagementMessageReaderClassifiesLocalBackpressure(t *testing.T) {
	node := &backpressuredLatestMessageNode{err: channelruntime.ErrBackpressured}
	reader := NewManagementMessageReader(node)

	_, err := reader.ListLocalLatestMessages(context.Background(), 0, 50)

	if !errors.Is(err, managementusecase.ErrLatestMessagesBackpressured) {
		t.Fatalf("ListLocalLatestMessages() error = %v, want latest-message backpressure", err)
	}
}

func TestManagementMessageReaderPreservesLocalLatestMessageContextErrors(t *testing.T) {
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(want.Error(), func(t *testing.T) {
			reader := NewManagementMessageReader(&backpressuredLatestMessageNode{err: want})

			_, err := reader.ListLocalLatestMessages(context.Background(), 0, 50)

			if !errors.Is(err, want) {
				t.Fatalf("ListLocalLatestMessages() error = %v, want %v identity", err, want)
			}
		})
	}
}

type recordingManagementMessageNode struct {
	channelID channelruntime.ChannelID
	req       channelstore.ReadCommittedRequest
	result    channelstore.ReadCommittedResult
	err       error
}

type backpressuredLatestMessageNode struct {
	recordingManagementMessageNode
	err error
}

func (n *backpressuredLatestMessageNode) NodeID() uint64 { return 1 }

func (n *backpressuredLatestMessageNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return control.Snapshot{}, nil
}

func (n *backpressuredLatestMessageNode) ReadLocalLatestMessages(context.Context, uint64, int) ([]channelruntime.Message, bool, uint64, error) {
	return nil, false, 0, n.err
}

func (n *backpressuredLatestMessageNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return nil, errors.New("unexpected RPC call")
}

func (n *recordingManagementMessageNode) ReadChannelCommitted(_ context.Context, id channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	n.channelID = id
	n.req = req
	return n.result, n.err
}

func sameManagementMessages(left, right []managementusecase.Message) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].MessageID != right[i].MessageID || left[i].MessageSeq != right[i].MessageSeq || left[i].ClientMsgNo != right[i].ClientMsgNo {
			return false
		}
		if left[i].ChannelID != right[i].ChannelID || left[i].ChannelType != right[i].ChannelType || left[i].FromUID != right[i].FromUID || left[i].Timestamp != right[i].Timestamp {
			return false
		}
		if string(left[i].Payload) != string(right[i].Payload) {
			return false
		}
	}
	return true
}
