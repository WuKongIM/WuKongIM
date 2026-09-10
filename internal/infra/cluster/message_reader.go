package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelMessageReadNode is the cluster committed message read surface used by internal.
type ChannelMessageReadNode interface {
	ReadChannelCommitted(context.Context, channelruntime.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error)
}

type channelMessageBatchReadNode interface {
	ReadChannelCommittedBatch(context.Context, []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error)
}

// MessageMembershipNode exposes UID-owned pull authorization state.
type MessageMembershipNode interface {
	GetUserChannelMembership(context.Context, string, string, int64) (metadb.UserChannelMembership, bool, error)
}

// MessageMembershipStore adapts cluster membership reads to message sync.
type MessageMembershipStore struct{ node MessageMembershipNode }

func NewMessageMembershipStore(node MessageMembershipNode) *MessageMembershipStore {
	return &MessageMembershipStore{node: node}
}

func (s *MessageMembershipStore) GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	if s == nil || s.node == nil {
		return metadb.UserChannelMembership{}, false, message.ErrSyncMembershipRequired
	}
	return s.node.GetUserChannelMembership(ctx, uid, channelID, channelType)
}

// CommittedMessageReader translates bounded record scans to cluster reads.
// Page selection, filtering and response ordering belong to message.PageReader.
type CommittedMessageReader struct {
	node ChannelMessageReadNode
}

// NewCommittedMessageReader creates the cluster adapter for message pages.
func NewCommittedMessageReader(node ChannelMessageReadNode) *CommittedMessageReader {
	return &CommittedMessageReader{node: node}
}

var _ message.CommittedMessageReader = (*CommittedMessageReader)(nil)

// ReadCommittedMessages preserves scan parameters, aligned errors and ownership
// while delegating exact Channel-Leader routing to the existing cluster batch.
func (r *CommittedMessageReader) ReadCommittedMessages(ctx context.Context, queries []message.CommittedMessageQuery) ([]message.CommittedMessageResult, error) {
	if r == nil || r.node == nil {
		return nil, message.ErrMessageReaderRequired
	}
	batchNode, ok := r.node.(channelMessageBatchReadNode)
	if !ok {
		return nil, message.ErrSyncBatchReaderRequired
	}
	reads := make([]clusterchannels.CommittedRead, len(queries))
	for index, query := range queries {
		reads[index] = clusterchannels.CommittedRead{
			ChannelID: channelruntime.ChannelID{ID: query.ChannelID.ID, Type: query.ChannelID.Type},
			Request: channelstore.ReadCommittedRequest{
				FromSeq: query.FromSeq, MinSeq: query.MinSeq, MaxSeq: query.MaxSeq,
				Limit: query.Limit, MaxBytes: query.MaxBytes, Reverse: query.Reverse,
			},
		}
	}
	readResults, err := batchNode.ReadChannelCommittedBatch(ctx, reads)
	if err != nil {
		return nil, mapAppendError(err)
	}
	if len(readResults) != len(queries) {
		return nil, message.ErrSyncBatchResultMismatch
	}
	results := make([]message.CommittedMessageResult, len(readResults))
	for index, read := range readResults {
		if read.Err != nil {
			results[index].Err = mapAppendError(read.Err)
			continue
		}
		results[index].Messages = committedMessagesFromChannel(read.Read.Messages)
	}
	return results, nil
}

func committedMessagesFromChannel(in []channelruntime.Message) []message.SyncedMessage {
	out := make([]message.SyncedMessage, len(in))
	for index, msg := range in {
		out[index] = message.SyncedMessage{
			Flags:     message.MessageFlags{SyncOnce: msg.SyncOnce, RedDot: msg.RedDot},
			MessageID: msg.MessageID, MessageSeq: msg.MessageSeq,
			ChannelID: msg.ChannelID, ChannelType: msg.ChannelType,
			Setting: msg.Setting, FromUID: msg.FromUID, ClientMsgNo: msg.ClientMsgNo, Expire: msg.Expire,
			Timestamp: int32(msg.ServerTimestampMS / 1000),
			Payload:   append([]byte(nil), msg.Payload...),
		}
	}
	return out
}

func maxUint64() uint64 {
	return ^uint64(0)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
