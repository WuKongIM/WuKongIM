package cluster

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

const cmdSyncReadPageLimit = 256

// CMDSyncNode exposes cluster reads and writes needed by CMD sync.
type CMDSyncNode interface {
	ListUserCMDChannelMembershipPage(context.Context, string, metadb.UserCMDChannelMembershipCursor, int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error)
	UpsertUserCMDChannelMemberships(context.Context, []metadb.UserCMDChannelMembership) error
	AdvanceUserCMDChannelMembershipAcks(context.Context, []metadb.UserCMDChannelMembership) error
	TombstoneUserCMDChannelMemberships(context.Context, []metadb.UserCMDChannelMembership) error
	CommittedChannelTail(context.Context, string, int64) (uint64, error)
	ReadPermissionMetadataBatchAuthoritative(context.Context, []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult
	ReadChannelCommittedBatch(context.Context, []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error)
}

// UpsertUserCMDChannelMemberships persists explicit durable CMD bindings.
func (s *CMDSyncStore) UpsertUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error {
	if len(memberships) == 0 {
		return nil
	}
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.UpsertUserCMDChannelMemberships(ctx, append([]metadb.UserCMDChannelMembership(nil), memberships...))
}

// CMDSyncStore adapts cluster CMD directory rows and command-channel logs.
type CMDSyncStore struct {
	node CMDSyncNode
	// CommandChannelSuffix must match the sending runtime and CMD sync usecase.
	CommandChannelSuffix string
}

var _ cmdsync.StateStore = (*CMDSyncStore)(nil)
var _ cmdsync.MessageStore = (*CMDSyncStore)(nil)

// NewCMDSyncStore creates a cluster-backed CMD sync store.
func NewCMDSyncStore(node CMDSyncNode) *CMDSyncStore {
	return &CMDSyncStore{node: node}
}

// ListUserCMDChannelMembershipPage reads the UID-owned CMD directory.
func (s *CMDSyncStore) ListUserCMDChannelMembershipPage(ctx context.Context, uid string, after metadb.UserCMDChannelMembershipCursor, limit int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error) {
	if s == nil || s.node == nil {
		return nil, metadb.UserCMDChannelMembershipCursor{}, false, metadb.ErrNotFound
	}
	return s.node.ListUserCMDChannelMembershipPage(ctx, uid, after, limit)
}

// AdvanceUserCMDChannelMembershipAcks advances CMD acknowledgement state.
func (s *CMDSyncStore) AdvanceUserCMDChannelMembershipAcks(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error {
	if len(memberships) == 0 {
		return nil
	}
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.AdvanceUserCMDChannelMembershipAcks(ctx, append([]metadb.UserCMDChannelMembership(nil), memberships...))
}

// TombstoneUserCMDChannelMemberships persists explicit durable CMD unbinds.
func (s *CMDSyncStore) TombstoneUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error {
	if len(memberships) == 0 {
		return nil
	}
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.TombstoneUserCMDChannelMemberships(ctx, append([]metadb.UserCMDChannelMembership(nil), memberships...))
}

// CommandChannelTail captures the committed boundary used by an explicit bind.
func (s *CMDSyncStore) CommandChannelTail(ctx context.Context, key cmdsync.CommandChannelKey) (uint64, error) {
	if s == nil || s.node == nil {
		return 0, metadb.ErrNotFound
	}
	return s.node.CommittedChannelTail(ctx, key.ChannelID, int64(key.ChannelType))
}

// LoadCommandMessages reads committed messages from one command-channel log.
func (s *CMDSyncStore) LoadCommandMessages(ctx context.Context, key cmdsync.CommandChannelKey, fromSeq uint64, limit int) ([]cmdsync.SyncedMessage, error) {
	rows, err := s.LoadCommandMessagesBatch(ctx, []cmdsync.CommandMessageRead{{Key: key, FromSeq: fromSeq, Limit: limit}})
	if err != nil {
		return nil, err
	}
	return rows[0], nil
}

// LoadCommandMessagesBatch shares Slot metadata reads and Channel-owner RPCs
// across a bounded directory chunk. It preserves source fences, per-channel
// pagination, and the explicit empty-log case without hiding unavailable reads.
func (s *CMDSyncStore) LoadCommandMessagesBatch(ctx context.Context, queries []cmdsync.CommandMessageRead) ([][]cmdsync.SyncedMessage, error) {
	if len(queries) > cmdsync.MaxCommandReadBatch {
		return nil, metadb.ErrInvalidArgument
	}
	out := make([][]cmdsync.SyncedMessage, len(queries))
	if len(queries) == 0 {
		return out, nil
	}
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	codec := runtimechannelid.CommandCodec{Suffix: s.CommandChannelSuffix}
	facts := make([]slotproxy.PermissionMetadataRead, len(queries))
	for i, q := range queries {
		source, _ := codec.FromCommandChannel(q.Key.ChannelID)
		facts[i] = slotproxy.PermissionMetadataRead{Kind: slotproxy.PermissionMetadataReadChannel, ChannelID: source, ChannelType: int64(q.Key.ChannelType)}
	}
	metadata := s.node.ReadPermissionMetadataBatchAuthoritative(ctx, facts)
	if len(metadata) != len(facts) {
		return nil, fmt.Errorf("CMD source batch returned %d rows for %d reads", len(metadata), len(facts))
	}
	for _, row := range metadata {
		if row.Err != nil && !errors.Is(row.Err, metadb.ErrNotFound) {
			return nil, row.Err
		}
		if row.Err == nil && row.Found && row.Channel.Disband != 0 {
			return nil, cmdsync.ErrChannelDisbanded
		}
	}
	next := make([]uint64, len(queries))
	limits := make([]int, len(queries))
	pending := make([]int, len(queries))
	for i, q := range queries {
		next[i] = max(q.FromSeq, 1)
		limits[i] = max(q.Limit, 1)
		pending[i] = i
	}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		requests := make([]clusterchannels.CommittedRead, len(pending))
		for j, i := range pending {
			q := queries[i]
			requests[j] = clusterchannels.CommittedRead{ChannelID: channelruntime.ChannelID{ID: q.Key.ChannelID, Type: q.Key.ChannelType}, Request: channelstore.ReadCommittedRequest{FromSeq: next[i], MaxSeq: maxUint64(), Limit: cmdSyncReadPageLimit, MaxBytes: maxInt()}}
		}
		results, err := s.node.ReadChannelCommittedBatch(ctx, requests)
		// A singleton not-found identifies exactly one unused log. An ambiguous
		// batch-level absence must not erase other channels' committed commands.
		if len(pending) == 1 && errors.Is(err, channelruntime.ErrChannelNotFound) {
			out[pending[0]] = nil
			break
		}
		if err != nil {
			return nil, mapAppendError(err)
		}
		if len(results) != len(requests) {
			return nil, fmt.Errorf("CMD read batch returned %d rows for %d reads", len(results), len(requests))
		}
		active := make([]int, 0, len(pending))
		for j, result := range results {
			i := pending[j]
			if errors.Is(result.Err, channelruntime.ErrChannelNotFound) {
				out[i] = nil
				continue
			}
			if result.Err != nil {
				return nil, mapAppendError(result.Err)
			}
			read := result.Read
			for _, msg := range read.Messages {
				out[i] = append(out[i], cmdSyncedMessageFromChannel(msg))
				if len(out[i]) >= limits[i] {
					break
				}
			}
			if len(out[i]) < limits[i] && len(read.Messages) > 0 && read.NextSeq > next[i] {
				next[i] = read.NextSeq
				active = append(active, i)
			}
		}
		pending = active
	}
	return out, nil
}

func cmdSyncedMessageFromChannel(msg channelruntime.Message) cmdsync.SyncedMessage {
	return cmdsync.SyncedMessage{
		MessageID:         msg.MessageID,
		MessageSeq:        msg.MessageSeq,
		ChannelID:         msg.ChannelID,
		ChannelType:       msg.ChannelType,
		FromUID:           msg.FromUID,
		ClientMsgNo:       msg.ClientMsgNo,
		ServerTimestampMS: msg.ServerTimestampMS,
		SyncOnce:          true,
		Payload:           append([]byte(nil), msg.Payload...),
	}
}
