package cluster

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
)

func TestChannelMessageReaderMapsPullUpRequestAndTrimsHasMore(t *testing.T) {
	node := &recordingReadNode{
		result: channelstore.ReadCommittedResult{Messages: []channelv2.Message{
			{MessageID: 10, MessageSeq: 2, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ClientMsgNo: "c1", Payload: []byte("a")},
			{MessageID: 11, MessageSeq: 3, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ClientMsgNo: "c2", Payload: []byte("b")},
			{MessageID: 12, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ClientMsgNo: "c3", Payload: []byte("c")},
		}},
	}
	reader := NewChannelMessageReader(node)

	page, err := reader.SyncMessages(context.Background(), message.ChannelMessageQuery{
		ChannelID: message.ChannelID{ID: "g1", Type: 2},
		StartSeq:  2,
		EndSeq:    5,
		Limit:     2,
		PullMode:  message.PullModeUp,
	})

	if err != nil {
		t.Fatalf("SyncMessages() error = %v", err)
	}
	if node.lastID != (channelv2.ChannelID{ID: "g1", Type: 2}) {
		t.Fatalf("channel id = %#v, want g1/2", node.lastID)
	}
	if node.lastReq.FromSeq != 2 || node.lastReq.MaxSeq != 4 || node.lastReq.Limit != 3 || node.lastReq.Reverse {
		t.Fatalf("read request = %#v, want forward 2..4 limit+1", node.lastReq)
	}
	if !page.HasMore || len(page.Messages) != 2 {
		t.Fatalf("page = %#v, want two messages with hasMore", page)
	}
	if page.Messages[0].MessageID != 10 || page.Messages[1].MessageID != 11 || string(page.Messages[0].Payload) != "a" {
		t.Fatalf("messages = %#v, want mapped first two messages", page.Messages)
	}
}

func TestChannelMessageReaderMapsPullDownAndReturnsAscending(t *testing.T) {
	node := &recordingReadNode{
		result: channelstore.ReadCommittedResult{Messages: []channelv2.Message{
			{MessageID: 15, MessageSeq: 5, ChannelID: "g1", ChannelType: 2},
			{MessageID: 14, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
			{MessageID: 13, MessageSeq: 3, ChannelID: "g1", ChannelType: 2},
		}},
	}
	reader := NewChannelMessageReader(node)

	page, err := reader.SyncMessages(context.Background(), message.ChannelMessageQuery{
		ChannelID: message.ChannelID{ID: "g1", Type: 2},
		StartSeq:  5,
		EndSeq:    2,
		Limit:     2,
		PullMode:  message.PullModeDown,
	})

	if err != nil {
		t.Fatalf("SyncMessages() error = %v", err)
	}
	if node.lastReq.FromSeq != 5 || node.lastReq.Limit != 3 || !node.lastReq.Reverse {
		t.Fatalf("read request = %#v, want reverse from 5 limit+1", node.lastReq)
	}
	if !page.HasMore || len(page.Messages) != 2 {
		t.Fatalf("page = %#v, want two messages with hasMore", page)
	}
	if page.Messages[0].MessageSeq != 4 || page.Messages[1].MessageSeq != 5 {
		t.Fatalf("messages = %#v, want ascending seq 4,5", page.Messages)
	}
}

type recordingReadNode struct {
	lastID  channelv2.ChannelID
	lastReq channelstore.ReadCommittedRequest
	result  channelstore.ReadCommittedResult
	err     error
}

func (n *recordingReadNode) ReadChannelCommitted(_ context.Context, id channelv2.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	n.lastID = id
	n.lastReq = req
	return n.result, n.err
}
