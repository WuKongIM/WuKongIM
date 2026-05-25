package app

import (
	"context"
	"errors"
	"strings"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type cmdsyncUIDOwnerRemote interface {
	SyncCMD(ctx context.Context, nodeID uint64, query cmdsync.SyncQuery) (cmdsync.SyncResult, error)
	SyncAckCMD(ctx context.Context, nodeID uint64, cmd cmdsync.SyncAckCommand) error
}

type cmdsyncUIDOwnerCluster interface {
	SlotForKey(string) multiraft.SlotID
	LeaderOf(multiraft.SlotID) (multiraft.NodeID, error)
}

// clusterCMDSyncUsecase routes legacy CMD sync calls to the UID slot owner.
type clusterCMDSyncUsecase struct {
	// local handles requests when this node owns the UID slot.
	local *cmdsync.App
	// remote forwards requests to another UID slot owner.
	remote cmdsyncUIDOwnerRemote
	// cluster resolves UID slots and their current leaders.
	cluster cmdsyncUIDOwnerCluster
	// localNodeID identifies the current node for owner comparisons.
	localNodeID uint64
}

func (u clusterCMDSyncUsecase) Sync(ctx context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	uid := strings.TrimSpace(query.UID)
	if uid == "" {
		return cmdsync.SyncResult{}, cmdsync.ErrUIDRequired
	}
	query.UID = uid

	ownerNodeID, err := u.ownerNodeID(uid)
	if err != nil {
		return cmdsync.SyncResult{}, err
	}
	if ownerNodeID == u.localNodeID || u.cluster == nil {
		if u.local == nil {
			return cmdsync.SyncResult{}, errors.New("app: cmd sync usecase not configured")
		}
		return u.local.Sync(ctx, query)
	}
	if u.remote == nil {
		return cmdsync.SyncResult{}, errors.New("app: cmd sync remote not configured")
	}
	return u.remote.SyncCMD(ctx, ownerNodeID, query)
}

func (u clusterCMDSyncUsecase) SyncAck(ctx context.Context, cmd cmdsync.SyncAckCommand) error {
	uid := strings.TrimSpace(cmd.UID)
	if uid == "" {
		return cmdsync.ErrUIDRequired
	}
	cmd.UID = uid

	ownerNodeID, err := u.ownerNodeID(uid)
	if err != nil {
		return err
	}
	if ownerNodeID == u.localNodeID || u.cluster == nil {
		if u.local == nil {
			return errors.New("app: cmd sync usecase not configured")
		}
		return u.local.SyncAck(ctx, cmd)
	}
	if u.remote == nil {
		return errors.New("app: cmd sync remote not configured")
	}
	return u.remote.SyncAckCMD(ctx, ownerNodeID, cmd)
}

func (u clusterCMDSyncUsecase) ownerNodeID(uid string) (uint64, error) {
	if u.cluster == nil {
		return u.localNodeID, nil
	}
	slotID := u.cluster.SlotForKey(uid)
	leaderID, err := u.cluster.LeaderOf(slotID)
	if err != nil {
		return 0, err
	}
	if leaderID == 0 {
		return 0, raftcluster.ErrNoLeader
	}
	return uint64(leaderID), nil
}

type cmdsyncMessageMetas interface {
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error)
}

type cmdsyncMessageRemote interface {
	QueryChannelMessages(ctx context.Context, nodeID uint64, req accessnode.ChannelMessagesQuery) (accessnode.ChannelMessagesPage, error)
}

type cmdsyncMessageStore struct {
	// localNodeID identifies the current node for command-channel log reads.
	localNodeID uint64
	// channelLog owns local committed command-channel message rows.
	channelLog *channelstore.Engine
	// metas resolves the authoritative leader for command-channel logs.
	metas cmdsyncMessageMetas
	// remote queries command-channel logs when another node is authoritative.
	remote cmdsyncMessageRemote
}

// LoadCommandMessages reads committed command-channel facts from the current
// command-channel leader without stripping the command suffix.
func (s cmdsyncMessageStore) LoadCommandMessages(ctx context.Context, key cmdsync.CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error) {
	if !runtimechannelid.IsCommandChannel(key.ChannelID) || key.ChannelType == 0 || limit <= 0 {
		return nil, channel.ErrInvalidArgument
	}
	if s.metas == nil {
		return nil, nil
	}

	id := channel.ChannelID{ID: key.ChannelID, Type: key.ChannelType}
	meta, err := s.metas.GetChannelRuntimeMeta(ctx, key.ChannelID, int64(key.ChannelType))
	if isMissingCommandLog(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if meta.Leader == 0 {
		return nil, raftcluster.ErrNoLeader
	}

	minAvailableSeq := channel.EffectiveMinAvailableSeq(meta.RetentionThroughSeq, 0)
	if meta.Leader == s.localNodeID {
		if s.channelLog == nil {
			return nil, nil
		}
		return s.loadLocal(ctx, id, fromSeq, limit, minAvailableSeq)
	}
	if s.remote == nil {
		return nil, channel.ErrStaleMeta
	}
	return s.loadRemote(ctx, meta.Leader, id, fromSeq, limit, minAvailableSeq)
}

func (s cmdsyncMessageStore) loadLocal(_ context.Context, id channel.ChannelID, fromSeq uint64, limit int, minAvailableSeq uint64) ([]channel.Message, error) {
	committedHW, err := channelhandler.LoadCommittedHW(s.channelLog, id)
	if isMissingCommandLog(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	page, err := channelhandler.SyncMessages(s.channelLog, committedHW, channelhandler.SyncMessagesRequest{
		ChannelID:       id,
		StartSeq:        fromSeq,
		Limit:           limit,
		PullMode:        channelhandler.SyncPullModeUp,
		MinAvailableSeq: minAvailableSeq,
	})
	if isMissingCommandLog(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cloneCommandMessages(page.Messages), nil
}

func (s cmdsyncMessageStore) loadRemote(ctx context.Context, nodeID uint64, id channel.ChannelID, fromSeq uint64, limit int, minAvailableSeq uint64) ([]channel.Message, error) {
	page, err := s.remote.QueryChannelMessages(ctx, nodeID, accessnode.ChannelMessagesQuery{
		ChannelID:       id,
		SyncMode:        true,
		StartSeq:        fromSeq,
		Limit:           limit,
		PullMode:        uint8(channelhandler.SyncPullModeUp),
		MinAvailableSeq: minAvailableSeq,
	})
	if isMissingCommandLog(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cloneCommandMessages(page.Messages), nil
}

func isMissingCommandLog(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, channel.ErrNotReady) || errors.Is(err, channel.ErrChannelNotFound) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, channel.ErrNotReady.Error()) ||
		strings.Contains(msg, channel.ErrChannelNotFound.Error())
}

func cloneCommandMessages(messages []channel.Message) []channel.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]channel.Message, len(messages))
	for i, msg := range messages {
		msg.Payload = append([]byte(nil), msg.Payload...)
		out[i] = msg
	}
	return out
}
