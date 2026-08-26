package cluster

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

func TestChannelMessageReaderMapsPullUpRequestAndTrimsHasMore(t *testing.T) {
	node := &recordingReadNode{
		batchResults: []clusterchannels.CommittedReadResult{{Read: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{
			{MessageID: 10, MessageSeq: 2, ChannelID: "g1", ChannelType: 2, Setting: 2, FromUID: "u1", ClientMsgNo: "c1", Payload: []byte("a")},
			{MessageID: 11, MessageSeq: 3, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ClientMsgNo: "c2", Payload: []byte("b")},
			{MessageID: 12, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ClientMsgNo: "c3", Payload: []byte("c")},
		}}}},
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
	if node.batchCalls != 1 || len(node.batchReads) != 1 || node.batchReads[0].ChannelID != (channelruntime.ChannelID{ID: "g1", Type: 2}) {
		t.Fatalf("batch calls=%d reads=%#v, want one g1/2 read", node.batchCalls, node.batchReads)
	}
	readReq := node.batchReads[0].Request
	if readReq.FromSeq != 2 || readReq.MaxSeq != 4 || readReq.Limit != 3 || readReq.Reverse {
		t.Fatalf("read request = %#v, want forward 2..4 limit+1", readReq)
	}
	if !page.HasMore || len(page.Messages) != 2 {
		t.Fatalf("page = %#v, want two messages with hasMore", page)
	}
	if page.Messages[0].MessageID != 10 || page.Messages[1].MessageID != 11 || string(page.Messages[0].Payload) != "a" {
		t.Fatalf("messages = %#v, want mapped first two messages", page.Messages)
	}
	if page.Messages[0].Setting != 2 {
		t.Fatalf("message setting = %d, want 2", page.Messages[0].Setting)
	}
}

func TestChannelMessageReaderMapsUnboundedPullUpRange(t *testing.T) {
	node := &recordingReadNode{batchResults: []clusterchannels.CommittedReadResult{{}}}
	reader := NewChannelMessageReader(node)

	_, err := reader.SyncMessages(context.Background(), message.ChannelMessageQuery{
		ChannelID: message.ChannelID{ID: "g1", Type: 2},
		StartSeq:  2,
		Limit:     10,
		PullMode:  message.PullModeUp,
	})
	if err != nil {
		t.Fatalf("SyncMessages() error = %v", err)
	}
	request := node.batchReads[0].Request
	if request.FromSeq != 2 || request.MaxSeq != maxUint64() || request.Reverse {
		t.Fatalf("read request = %#v, want forward [2,+inf)", request)
	}
}

func TestChannelMessageReaderMapsPullDownAndReturnsAscending(t *testing.T) {
	node := &recordingReadNode{
		batchResults: []clusterchannels.CommittedReadResult{{Read: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{
			{MessageID: 15, MessageSeq: 5, ChannelID: "g1", ChannelType: 2},
			{MessageID: 14, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
			{MessageID: 13, MessageSeq: 3, ChannelID: "g1", ChannelType: 2},
		}}}},
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
	readReq := node.batchReads[0].Request
	if readReq.FromSeq != 5 || readReq.Limit != 3 || !readReq.Reverse {
		t.Fatalf("read request = %#v, want reverse from 5 limit+1", readReq)
	}
	if !page.HasMore || len(page.Messages) != 2 {
		t.Fatalf("page = %#v, want two messages with hasMore", page)
	}
	if page.Messages[0].MessageSeq != 4 || page.Messages[1].MessageSeq != 5 {
		t.Fatalf("messages = %#v, want ascending seq 4,5", page.Messages)
	}
}

func TestChannelMessageReaderPreservesLegacyMessageTimestamp(t *testing.T) {
	node := &recordingReadNode{
		batchResults: []clusterchannels.CommittedReadResult{{
			Read: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{{
				MessageID: 1, MessageSeq: 1, ChannelID: "g1", ChannelType: 2,
				ServerTimestampMS: 1_700_000_000_123,
			}}},
		}},
	}
	reader := NewChannelMessageReader(node)

	page, err := reader.SyncMessages(context.Background(), message.ChannelMessageQuery{
		ChannelID: message.ChannelID{ID: "g1", Type: 2}, Limit: 1, PullMode: message.PullModeDown,
	})
	if err != nil {
		t.Fatalf("SyncMessages(): %v", err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Timestamp != 1_700_000_000 {
		t.Fatalf("messages = %#v, want durable server timestamp in legacy seconds", page.Messages)
	}
}

func TestChannelMessageReaderSingleUsesRoutedOneItemBatch(t *testing.T) {
	node := &recordingReadNode{batchResults: []clusterchannels.CommittedReadResult{{
		Read: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{{
			MessageID: 10, MessageSeq: 1, ChannelID: "g1", ChannelType: 2, ClientMsgNo: "routed",
		}},
		}}}}
	reader := NewChannelMessageReader(node)

	page, err := reader.SyncMessages(context.Background(), message.ChannelMessageQuery{
		ChannelID: message.ChannelID{ID: "g1", Type: 2},
		StartSeq:  1,
		Limit:     10,
		PullMode:  message.PullModeDown,
	})
	if err != nil {
		t.Fatalf("SyncMessages() error = %v", err)
	}
	if node.batchCalls != 1 || len(node.batchReads) != 1 {
		t.Fatalf("batch calls=%d reads=%+v, want one routed item", node.batchCalls, node.batchReads)
	}
	if node.lastID != (channelruntime.ChannelID{}) {
		t.Fatalf("local ReadChannelCommitted called with %v", node.lastID)
	}
	if len(page.Messages) != 1 || page.Messages[0].ClientMsgNo != "routed" {
		t.Fatalf("page=%+v, want routed message", page)
	}
}

func TestChannelMessageReaderBatchUsesOneAlignedClusterRead(t *testing.T) {
	node := &recordingReadNode{batchResults: []clusterchannels.CommittedReadResult{
		{Read: channelstore.ReadCommittedResult{Messages: []channelruntime.Message{{MessageSeq: 2}, {MessageSeq: 3}}}},
		{Err: channelruntime.ErrNotReady},
	}}
	reader := NewChannelMessageReader(node)

	results, err := reader.SyncMessagesBatch(context.Background(), []message.ChannelMessageQuery{
		{ChannelID: message.ChannelID{ID: "g1", Type: 2}, StartSeq: 1, Limit: 1, PullMode: message.PullModeUp},
		{ChannelID: message.ChannelID{ID: "g2", Type: 2}, StartSeq: 4, Limit: 2, PullMode: message.PullModeUp},
	})
	if err != nil {
		t.Fatalf("SyncMessagesBatch() error=%v", err)
	}
	if node.batchCalls != 1 || len(node.batchReads) != 2 {
		t.Fatalf("batch calls=%d reads=%+v", node.batchCalls, node.batchReads)
	}
	if !results[0].Page.HasMore || len(results[0].Page.Messages) != 1 {
		t.Fatalf("first result=%+v, want trimmed page", results[0])
	}
	if results[1].Err == nil {
		t.Fatalf("second result=%+v, want item error", results[1])
	}
}

type recordingReadNode struct {
	lastID       channelruntime.ChannelID
	lastReq      channelstore.ReadCommittedRequest
	result       channelstore.ReadCommittedResult
	err          error
	batchCalls   int
	batchReads   []clusterchannels.CommittedRead
	batchResults []clusterchannels.CommittedReadResult
}

func (n *recordingReadNode) ReadChannelCommittedBatch(_ context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.batchCalls++
	n.batchReads = append([]clusterchannels.CommittedRead(nil), reads...)
	return n.batchResults, n.err
}

func (n *recordingReadNode) ReadChannelCommitted(_ context.Context, id channelruntime.ChannelID, req channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error) {
	n.lastID = id
	n.lastReq = req
	return n.result, n.err
}
