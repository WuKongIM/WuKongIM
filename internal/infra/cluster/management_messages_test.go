package cluster

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
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

type recordingManagementMessageNode struct {
	channelID channelruntime.ChannelID
	req       channelstore.ReadCommittedRequest
	result    channelstore.ReadCommittedResult
	err       error
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
