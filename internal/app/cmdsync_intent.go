package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// cmdConversationIntentRemote forwards owner-routed pending CMD conversation intents.
type cmdConversationIntentRemote interface {
	PushCMDConversationIntent(ctx context.Context, nodeID uint64, intent cmdsync.ConversationIntent) error
}

// cmdConversationIntentLocal stores pending CMD conversation intents on the local owner.
type cmdConversationIntentLocal interface {
	PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) error
}

type cmdConversationIntentSink interface {
	PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) (bool, error)
}

// cmdConversationIntentRouter partitions a conversation intent by UID owner.
type cmdConversationIntentRouter struct {
	// local stores owner-local UID partitions.
	local cmdConversationIntentLocal
	// remote forwards UID partitions owned by other nodes.
	remote cmdConversationIntentRemote
	// cluster resolves each UID's slot leader; nil means every UID is local.
	cluster cmdsyncUIDOwnerCluster
	// localNodeID identifies this node for owner comparisons.
	localNodeID uint64
	// logger is reserved for future app-level routing diagnostics.
	logger wklog.Logger
}

// PushIntent validates, partitions, and routes one CMD conversation intent.
func (r cmdConversationIntentRouter) PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) (fullyAccepted bool, err error) {
	normalized, err := normalizeCMDConversationIntentForRouting(intent)
	if err != nil {
		return false, err
	}

	groups, err := r.groupCMDConversationIntentByOwner(normalized)
	if err != nil {
		return false, err
	}

	var errs []error
	for ownerNodeID, readSeqs := range groups {
		groupIntent := cloneCMDConversationIntentWithReadSeqs(normalized, readSeqs)
		if err := r.pushCMDConversationIntentGroup(ctx, ownerNodeID, groupIntent); err != nil {
			if ownerNodeID != r.localNodeID && errors.Is(err, cmdsync.ErrConversationIntentStaleOwner) {
				if retryErr := r.retryStaleCMDConversationIntentGroup(ctx, normalized, readSeqs); retryErr == nil {
					continue
				} else {
					errs = append(errs, retryErr)
					continue
				}
			}
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return false, errors.Join(errs...)
	}
	return true, nil
}

// cmdConversationResolvedUIDObserver projects delivery-resolved UID pages into CMD conversation intents.
type cmdConversationResolvedUIDObserver struct {
	// sink routes intents to UID owners.
	sink cmdConversationIntentSink
	// now supplies fallback timestamps for messages without committed timestamps.
	now func() time.Time
	// logger receives non-fatal observer diagnostics.
	logger wklog.Logger
}

// OnResolvedUIDPage builds one intent from an already-resolved UID page.
func (o cmdConversationResolvedUIDObserver) OnResolvedUIDPage(ctx context.Context, page resolvedUIDPage) {
	if o.sink == nil || page.Envelope.CMDConversationIntentSubmitted {
		return
	}
	intent, ok := cmdsync.BuildConversationIntent(page.Envelope.Message, page.UIDs, o.now)
	if !ok {
		return
	}
	if _, err := o.sink.PushIntent(ctx, intent); err != nil && o.logger != nil {
		o.logger.Warn("cmd conversation intent route failed",
			wklog.Event("cmdsync.intent.route.failed"),
			wklog.Error(err),
		)
	}
}

func (r cmdConversationIntentRouter) pushCMDConversationIntentGroup(ctx context.Context, ownerNodeID uint64, intent cmdsync.ConversationIntent) error {
	if ownerNodeID == r.localNodeID || r.cluster == nil {
		if r.local == nil {
			return errors.New("app: cmd conversation intent local not configured")
		}
		return r.local.PushIntent(ctx, intent)
	}
	if r.remote == nil {
		return errors.New("app: cmd conversation intent remote not configured")
	}
	return r.remote.PushCMDConversationIntent(ctx, ownerNodeID, intent)
}

func (r cmdConversationIntentRouter) retryStaleCMDConversationIntentGroup(ctx context.Context, base cmdsync.ConversationIntent, readSeqs map[string]uint64) error {
	groups, err := r.groupReadSeqsByOwner(readSeqs)
	if err != nil {
		return err
	}
	var errs []error
	for ownerNodeID, retryReadSeqs := range groups {
		intent := cloneCMDConversationIntentWithReadSeqs(base, retryReadSeqs)
		if err := r.pushCMDConversationIntentGroup(ctx, ownerNodeID, intent); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r cmdConversationIntentRouter) groupCMDConversationIntentByOwner(intent cmdsync.ConversationIntent) (map[uint64]map[string]uint64, error) {
	return r.groupReadSeqsByOwner(intent.UserReadSeqs)
}

func (r cmdConversationIntentRouter) groupReadSeqsByOwner(readSeqs map[string]uint64) (map[uint64]map[string]uint64, error) {
	groups := make(map[uint64]map[string]uint64)
	for uid, readSeq := range readSeqs {
		ownerNodeID, err := r.ownerNodeID(uid)
		if err != nil {
			return nil, err
		}
		group := groups[ownerNodeID]
		if group == nil {
			group = make(map[string]uint64)
			groups[ownerNodeID] = group
		}
		group[uid] = readSeq
	}
	return groups, nil
}

func (r cmdConversationIntentRouter) ownerNodeID(uid string) (uint64, error) {
	if r.cluster == nil {
		return r.localNodeID, nil
	}
	slotID := r.cluster.SlotForKey(uid)
	leaderID, err := r.cluster.LeaderOf(slotID)
	if err != nil {
		return 0, err
	}
	if leaderID == 0 {
		return 0, raftcluster.ErrNoLeader
	}
	return uint64(leaderID), nil
}

// ownerValidatingCMDIntentSink rejects node-RPC intents not owned by this node.
type ownerValidatingCMDIntentSink struct {
	// local stores the full intent after all UID owners validate as local.
	local cmdConversationIntentLocal
	// cluster resolves each UID's current owner; nil means every UID is local.
	cluster cmdsyncUIDOwnerCluster
	// localNodeID identifies the only owner accepted by this wrapper.
	localNodeID uint64
}

// PushIntent validates that every UID belongs to the local node before storing.
func (s ownerValidatingCMDIntentSink) PushIntent(ctx context.Context, intent cmdsync.ConversationIntent) error {
	normalized, err := normalizeCMDConversationIntentForRouting(intent)
	if err != nil {
		return err
	}
	for uid := range normalized.UserReadSeqs {
		ownerNodeID, err := s.ownerNodeID(uid)
		if err != nil {
			return err
		}
		if ownerNodeID != s.localNodeID {
			return cmdsync.ErrConversationIntentStaleOwner
		}
	}
	if s.local == nil {
		return errors.New("app: cmd conversation intent local not configured")
	}
	return s.local.PushIntent(ctx, normalized)
}

func (s ownerValidatingCMDIntentSink) ownerNodeID(uid string) (uint64, error) {
	if s.cluster == nil {
		return s.localNodeID, nil
	}
	slotID := s.cluster.SlotForKey(uid)
	leaderID, err := s.cluster.LeaderOf(slotID)
	if err != nil {
		return 0, err
	}
	if leaderID == 0 {
		return 0, raftcluster.ErrNoLeader
	}
	return uint64(leaderID), nil
}

func normalizeCMDConversationIntentForRouting(intent cmdsync.ConversationIntent) (cmdsync.ConversationIntent, error) {
	commandChannelID := strings.TrimSpace(intent.CommandChannelID)
	if intent.MessageSeq == 0 || commandChannelID == "" || !runtimechannelid.IsCommandChannel(commandChannelID) || intent.ChannelType == 0 {
		return cmdsync.ConversationIntent{}, cmdsync.ErrIntentRequired
	}
	if len(intent.UserReadSeqs) == 0 {
		return cmdsync.ConversationIntent{}, cmdsync.ErrIntentRequired
	}

	readSeqs := make(map[string]uint64, len(intent.UserReadSeqs))
	for uid, readSeq := range intent.UserReadSeqs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			return cmdsync.ConversationIntent{}, cmdsync.ErrIntentRequired
		}
		if readSeq > intent.MessageSeq {
			return cmdsync.ConversationIntent{}, fmt.Errorf("%w: read seq exceeds message seq", cmdsync.ErrIntentRequired)
		}
		if current, ok := readSeqs[uid]; !ok || readSeq > current {
			readSeqs[uid] = readSeq
		}
	}
	if len(readSeqs) == 0 {
		return cmdsync.ConversationIntent{}, cmdsync.ErrIntentRequired
	}

	intent.CommandChannelID = commandChannelID
	intent.SenderUID = strings.TrimSpace(intent.SenderUID)
	intent.UserReadSeqs = readSeqs
	return intent, nil
}

func cloneCMDConversationIntentWithReadSeqs(intent cmdsync.ConversationIntent, readSeqs map[string]uint64) cmdsync.ConversationIntent {
	intent.UserReadSeqs = cloneCMDConversationReadSeqs(readSeqs)
	return intent
}

func cloneCMDConversationReadSeqs(readSeqs map[string]uint64) map[string]uint64 {
	if len(readSeqs) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(readSeqs))
	for uid, readSeq := range readSeqs {
		out[uid] = readSeq
	}
	return out
}
