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

// ChannelMessageReader adapts cluster committed reads to the message usecase sync port.
type ChannelMessageReader struct {
	node ChannelMessageReadNode
}

// NewChannelMessageReader creates a ChannelMessageReader.
func NewChannelMessageReader(node ChannelMessageReadNode) *ChannelMessageReader {
	return &ChannelMessageReader{node: node}
}

// SyncMessages returns one compatible channel message page.
func (r *ChannelMessageReader) SyncMessages(ctx context.Context, query message.ChannelMessageQuery) (message.ChannelMessagePage, error) {
	if r == nil || r.node == nil {
		return message.ChannelMessagePage{}, message.ErrMessageReaderRequired
	}
	batchNode, ok := r.node.(channelMessageBatchReadNode)
	if !ok {
		return message.ChannelMessagePage{}, message.ErrSyncBatchReaderRequired
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1
	}
	results, err := batchNode.ReadChannelCommittedBatch(ctx, []clusterchannels.CommittedRead{{
		ChannelID: channelruntime.ChannelID{ID: query.ChannelID.ID, Type: query.ChannelID.Type},
		Request:   readCommittedRequest(query, limit),
	}})
	if err != nil {
		return message.ChannelMessagePage{}, mapAppendError(err)
	}
	if len(results) != 1 {
		return message.ChannelMessagePage{}, message.ErrSyncBatchResultMismatch
	}
	if results[0].Err != nil {
		return message.ChannelMessagePage{}, mapAppendError(results[0].Err)
	}
	return channelMessagePageFromRead(query, limit, results[0].Read), nil
}

// SyncMessagesBatch performs one Channel-Leader-grouped cluster read and
// preserves one item-scoped result for every query.
func (r *ChannelMessageReader) SyncMessagesBatch(ctx context.Context, queries []message.ChannelMessageQuery) ([]message.ChannelMessageReadResult, error) {
	if r == nil || r.node == nil {
		return nil, message.ErrMessageReaderRequired
	}
	batchNode, ok := r.node.(channelMessageBatchReadNode)
	if !ok {
		return nil, message.ErrSyncBatchReaderRequired
	}
	reads := make([]clusterchannels.CommittedRead, len(queries))
	limits := make([]int, len(queries))
	for index, query := range queries {
		limit := query.Limit
		if limit <= 0 {
			limit = 1
		}
		limits[index] = limit
		reads[index] = clusterchannels.CommittedRead{
			ChannelID: channelruntime.ChannelID{ID: query.ChannelID.ID, Type: query.ChannelID.Type},
			Request:   readCommittedRequest(query, limit),
		}
	}
	readResults, err := batchNode.ReadChannelCommittedBatch(ctx, reads)
	if err != nil {
		return nil, mapAppendError(err)
	}
	if len(readResults) != len(queries) {
		return nil, message.ErrSyncBatchResultMismatch
	}
	results := make([]message.ChannelMessageReadResult, len(queries))
	for index, readResult := range readResults {
		if readResult.Err != nil {
			results[index].Err = mapAppendError(readResult.Err)
			continue
		}
		results[index].Page = channelMessagePageFromRead(queries[index], limits[index], readResult.Read)
	}
	return results, nil
}

func channelMessagePageFromRead(query message.ChannelMessageQuery, limit int, read channelstore.ReadCommittedResult) message.ChannelMessagePage {
	messages := syncedMessagesFromChannel(read.Messages)
	messages = filterSyncedMessages(query, messages)
	reverse := query.PullMode == message.PullModeDown || (query.StartSeq == 0 && query.EndSeq == 0)
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}
	if reverse {
		reverseSyncedMessages(messages)
	}
	return message.ChannelMessagePage{Messages: messages, HasMore: hasMore}
}

func readCommittedRequest(query message.ChannelMessageQuery, limit int) channelstore.ReadCommittedRequest {
	req := channelstore.ReadCommittedRequest{
		FromSeq:  query.StartSeq,
		MaxSeq:   queryMaxSeq(query),
		MinSeq:   query.MinSeq,
		Limit:    limit + 1,
		MaxBytes: maxInt(),
	}
	if query.PullMode == message.PullModeDown || (query.StartSeq == 0 && query.EndSeq == 0) {
		req.Reverse = true
		if req.FromSeq == 0 {
			req.FromSeq = maxUint64()
			req.MaxSeq = maxUint64()
		}
	}
	if req.FromSeq == 0 && !req.Reverse {
		req.FromSeq = 1
	}
	return req
}

func queryMaxSeq(query message.ChannelMessageQuery) uint64 {
	if query.PullMode == message.PullModeUp && query.EndSeq > 0 {
		return query.EndSeq - 1
	}
	if query.PullMode == message.PullModeDown && query.StartSeq > 0 {
		return query.StartSeq
	}
	return maxUint64()
}

func syncedMessagesFromChannel(in []channelruntime.Message) []message.SyncedMessage {
	out := make([]message.SyncedMessage, 0, len(in))
	for _, msg := range in {
		if msg.SyncOnce {
			continue
		}
		out = append(out, message.SyncedMessage{
			MessageID:   msg.MessageID,
			MessageSeq:  msg.MessageSeq,
			ChannelID:   msg.ChannelID,
			ChannelType: msg.ChannelType,
			Setting:     msg.Setting,
			FromUID:     msg.FromUID,
			ClientMsgNo: msg.ClientMsgNo,
			Payload:     append([]byte(nil), msg.Payload...),
		})
	}
	return out
}

func filterSyncedMessages(query message.ChannelMessageQuery, messages []message.SyncedMessage) []message.SyncedMessage {
	if query.PullMode == message.PullModeDown && query.EndSeq > 0 {
		kept := messages[:0]
		for _, msg := range messages {
			if msg.MessageSeq <= query.EndSeq {
				continue
			}
			kept = append(kept, msg)
		}
		return kept
	}
	if query.PullMode == message.PullModeUp && query.EndSeq > 0 {
		kept := messages[:0]
		for _, msg := range messages {
			if msg.MessageSeq >= query.EndSeq {
				continue
			}
			kept = append(kept, msg)
		}
		return kept
	}
	return messages
}

func reverseSyncedMessages(messages []message.SyncedMessage) {
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
}

func maxUint64() uint64 {
	return ^uint64(0)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
