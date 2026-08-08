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
)

const cmdSyncReadPageLimit = 256

// CMDSyncNode exposes cluster reads and writes needed by CMD sync.
type CMDSyncNode interface {
	ListUserCMDChannelMembershipPage(context.Context, string, metadb.UserCMDChannelMembershipCursor, int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error)
	UpsertUserCMDChannelMemberships(context.Context, []metadb.UserCMDChannelMembership) error
	AdvanceUserCMDChannelMembershipAcks(context.Context, []metadb.UserCMDChannelMembership) error
	TombstoneUserCMDChannelMemberships(context.Context, []metadb.UserCMDChannelMembership) error
	CommittedChannelTail(context.Context, string, int64) (uint64, error)
	GetChannelMetadataAuthoritative(context.Context, string, int64) (metadb.Channel, error)
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
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	if limit <= 0 {
		limit = 1
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	sourceChannelID, _ := runtimechannelid.FromCommandChannel(key.ChannelID)
	channel, err := s.node.GetChannelMetadataAuthoritative(ctx, sourceChannelID, int64(key.ChannelType))
	if err != nil && !errors.Is(err, metadb.ErrNotFound) {
		return nil, err
	}
	if err == nil && channel.Disband != 0 {
		return nil, cmdsync.ErrChannelDisbanded
	}
	out := make([]cmdsync.SyncedMessage, 0, limit)
	nextSeq := fromSeq
	for len(out) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reads, err := s.node.ReadChannelCommittedBatch(ctx, []clusterchannels.CommittedRead{{
			ChannelID: channelruntime.ChannelID{ID: key.ChannelID, Type: key.ChannelType},
			Request: channelstore.ReadCommittedRequest{
				FromSeq:  nextSeq,
				MaxSeq:   maxUint64(),
				Limit:    cmdSyncReadPageLimit,
				MaxBytes: maxInt(),
			},
		}})
		if err != nil {
			return nil, mapAppendError(err)
		}
		if len(reads) != 1 {
			return nil, fmt.Errorf("cmd sync: routed read result count %d, want 1", len(reads))
		}
		if reads[0].Err != nil {
			return nil, mapAppendError(reads[0].Err)
		}
		read := reads[0].Read
		if len(read.Messages) == 0 {
			break
		}
		for _, msg := range read.Messages {
			out = append(out, cmdSyncedMessageFromChannel(msg))
			if len(out) >= limit {
				break
			}
		}
		if read.NextSeq <= nextSeq {
			break
		}
		nextSeq = read.NextSeq
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
