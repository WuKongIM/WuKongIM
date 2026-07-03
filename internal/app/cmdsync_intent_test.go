package app

import (
	"context"
	"errors"
	"testing"
	"time"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestCMDIntentRouterSplitsByUIDOwner(t *testing.T) {
	local := &recordingCMDIntentSink{}
	remote := &recordingCMDIntentRemote{}
	cluster := newStaticUIDOwnerCluster(map[string]uint64{"u1": 1, "u2": 2})
	router := cmdConversationIntentRouter{local: local, remote: remote, cluster: cluster, localNodeID: 1}

	accepted, err := router.PushIntent(context.Background(), validCMDConversationIntent(map[string]uint64{"u1": 9, "u2": 0}))

	require.NoError(t, err)
	require.True(t, accepted)
	require.Len(t, local.intents, 1)
	require.Equal(t, map[string]uint64{"u1": 9}, local.intents[0].UserReadSeqs)
	require.Len(t, remote.calls, 1)
	require.Equal(t, uint64(2), remote.calls[0].nodeID)
	require.Equal(t, map[string]uint64{"u2": 0}, remote.calls[0].intent.UserReadSeqs)
}

func TestCMDIntentRouterPartialFailureReturnsNotFullyAccepted(t *testing.T) {
	local := &recordingCMDIntentSink{}
	remote := &recordingCMDIntentRemote{errs: []error{errors.New("remote unavailable")}}
	cluster := newStaticUIDOwnerCluster(map[string]uint64{"u1": 1, "u2": 2})
	router := cmdConversationIntentRouter{local: local, remote: remote, cluster: cluster, localNodeID: 1}

	accepted, err := router.PushIntent(context.Background(), validCMDConversationIntent(map[string]uint64{"u1": 9, "u2": 0}))

	require.Error(t, err)
	require.False(t, accepted)
	require.Len(t, local.intents, 1)
	require.Equal(t, map[string]uint64{"u1": 9}, local.intents[0].UserReadSeqs)
	require.Len(t, remote.calls, 1)
	require.Equal(t, map[string]uint64{"u2": 0}, remote.calls[0].intent.UserReadSeqs)
}

func TestCMDIntentRouterPartialFailureCanRetryFullIntentIdempotently(t *testing.T) {
	local := cmdsync.NewConversationUpdater(cmdsync.ConversationUpdaterOptions{})
	remote := &recordingCMDIntentRemote{errs: []error{errors.New("remote unavailable")}}
	cluster := newStaticUIDOwnerCluster(map[string]uint64{"u1": 1, "u2": 2})
	router := cmdConversationIntentRouter{local: local, remote: remote, cluster: cluster, localNodeID: 1}
	intent := validCMDConversationIntent(map[string]uint64{"u1": 9, "u2": 0})

	accepted, err := router.PushIntent(context.Background(), intent)
	require.Error(t, err)
	require.False(t, accepted)
	require.Equal(t, []cmdsync.PendingConversationView{{CommandChannelID: intent.CommandChannelID, ChannelType: intent.ChannelType, LastMsgSeq: 9, ActiveAt: intent.ActiveAt, ReadSeq: 9}}, local.ListPending(context.Background(), "u1", 10))

	accepted, err = router.PushIntent(context.Background(), intent)
	require.NoError(t, err)
	require.True(t, accepted)
	require.Len(t, remote.calls, 2)
	require.Equal(t, []cmdsync.PendingConversationView{{CommandChannelID: intent.CommandChannelID, ChannelType: intent.ChannelType, LastMsgSeq: 9, ActiveAt: intent.ActiveAt, ReadSeq: 9}}, local.ListPending(context.Background(), "u1", 10))
}

func TestCMDConversationIntentOwnerValidationRejectsStaleOwnerWithoutPartialStore(t *testing.T) {
	local := &recordingCMDIntentSink{}
	cluster := newStaticUIDOwnerCluster(map[string]uint64{"u1": 1, "u2": 2})
	sink := ownerValidatingCMDIntentSink{local: local, cluster: cluster, localNodeID: 1}

	err := sink.PushIntent(context.Background(), validCMDConversationIntent(map[string]uint64{"u1": 9, "u2": 0}))

	require.ErrorIs(t, err, cmdsync.ErrConversationIntentStaleOwner)
	require.Empty(t, local.intents)
}

func TestCMDIntentRouterRetriesStaleOwnerAfterReresolve(t *testing.T) {
	local := &recordingCMDIntentSink{}
	remote := &recordingCMDIntentRemote{errs: []error{cmdsync.ErrConversationIntentStaleOwner}}
	cluster := newStaticUIDOwnerCluster(map[string]uint64{"u1": 2})
	cluster.setLeaderSequence("u1", 2, 1)
	router := cmdConversationIntentRouter{local: local, remote: remote, cluster: cluster, localNodeID: 1}

	accepted, err := router.PushIntent(context.Background(), validCMDConversationIntent(map[string]uint64{"u1": 0}))

	require.NoError(t, err)
	require.True(t, accepted)
	require.Len(t, remote.calls, 1)
	require.Equal(t, uint64(2), remote.calls[0].nodeID)
	require.Len(t, local.intents, 1)
	require.Equal(t, map[string]uint64{"u1": 0}, local.intents[0].UserReadSeqs)
}

func TestCMDIntentRouterValidatesBeforeRouting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(cmdsync.ConversationIntent) cmdsync.ConversationIntent
	}{
		{name: "zero message seq", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.MessageSeq = 0
			return intent
		}},
		{name: "empty command channel", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.CommandChannelID = ""
			return intent
		}},
		{name: "non command channel", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.CommandChannelID = "g1"
			return intent
		}},
		{name: "zero channel type", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.ChannelType = 0
			return intent
		}},
		{name: "nil uid map", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.UserReadSeqs = nil
			return intent
		}},
		{name: "empty uid map", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.UserReadSeqs = map[string]uint64{}
			return intent
		}},
		{name: "blank uid", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.UserReadSeqs = map[string]uint64{" ": 0}
			return intent
		}},
		{name: "read seq beyond message seq", mutate: func(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
			intent.UserReadSeqs = map[string]uint64{"u1": intent.MessageSeq + 1}
			return intent
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local := &recordingCMDIntentSink{}
			remote := &recordingCMDIntentRemote{}
			router := cmdConversationIntentRouter{
				local:       local,
				remote:      remote,
				cluster:     newStaticUIDOwnerCluster(map[string]uint64{"u1": 1}),
				localNodeID: 1,
			}

			accepted, err := router.PushIntent(context.Background(), tc.mutate(validCMDConversationIntent(map[string]uint64{"u1": 0})))

			require.Error(t, err)
			require.False(t, accepted)
			require.Empty(t, local.intents)
			require.Empty(t, remote.calls)
		})
	}
}

func TestCMDConversationResolvedUIDObserverBuildsIntent(t *testing.T) {
	sink := &recordingRoutedCMDIntentSink{accepted: true}
	observer := cmdConversationResolvedUIDObserver{
		sink: sink,
		now:  func() time.Time { return time.Unix(123, 0) },
	}

	observer.OnResolvedUIDPage(context.Background(), resolvedUIDPage{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{
			ChannelID:   channelid.ToCommandChannel("g1"),
			ChannelType: frame.ChannelTypeGroup,
			MessageSeq:  9,
			FromUID:     "u1",
		}},
		UIDs: []string{"u1", "u2", "u2"},
	})

	require.Len(t, sink.intents, 1)
	require.Equal(t, channelid.ToCommandChannel("g1"), sink.intents[0].CommandChannelID)
	require.Equal(t, map[string]uint64{"u1": 9, "u2": 0}, sink.intents[0].UserReadSeqs)
}

func TestCMDConversationResolvedUIDObserverSkipsAlreadySubmittedOrInvalidPages(t *testing.T) {
	sink := &recordingRoutedCMDIntentSink{accepted: true}
	observer := cmdConversationResolvedUIDObserver{sink: sink}

	observer.OnResolvedUIDPage(context.Background(), resolvedUIDPage{
		Envelope: deliveryruntime.CommittedEnvelope{
			Message: channel.Message{
				ChannelID:   channelid.ToCommandChannel("g1"),
				ChannelType: frame.ChannelTypeGroup,
				MessageSeq:  9,
			},
			CMDConversationIntentSubmitted: true,
		},
		UIDs: []string{"u1"},
	})
	observer.OnResolvedUIDPage(context.Background(), resolvedUIDPage{
		Envelope: deliveryruntime.CommittedEnvelope{Message: channel.Message{
			ChannelID:   channelid.ToCommandChannel("g1"),
			ChannelType: frame.ChannelTypeGroup,
			MessageSeq:  0,
		}},
		UIDs: []string{"u1"},
	})

	require.Empty(t, sink.intents)
}

func validCMDConversationIntent(readSeqs map[string]uint64) cmdsync.ConversationIntent {
	return cmdsync.ConversationIntent{
		CommandChannelID: channelid.ToCommandChannel("g1"),
		ChannelType:      frame.ChannelTypeGroup,
		MessageSeq:       9,
		ActiveAt:         100,
		SenderUID:        "u1",
		UserReadSeqs:     readSeqs,
	}
}

type recordingCMDIntentSink struct {
	intents []cmdsync.ConversationIntent
	err     error
}

func (s *recordingCMDIntentSink) PushIntent(_ context.Context, intent cmdsync.ConversationIntent) error {
	if s.err != nil {
		return s.err
	}
	s.intents = append(s.intents, cloneTestCMDConversationIntent(intent))
	return nil
}

type recordingRoutedCMDIntentSink struct {
	intents  []cmdsync.ConversationIntent
	accepted bool
	err      error
}

func (s *recordingRoutedCMDIntentSink) PushIntent(_ context.Context, intent cmdsync.ConversationIntent) (bool, error) {
	s.intents = append(s.intents, cloneTestCMDConversationIntent(intent))
	return s.accepted, s.err
}

type recordingCMDIntentRemote struct {
	calls []recordingCMDIntentRemoteCall
	errs  []error
}

type recordingCMDIntentRemoteCall struct {
	nodeID uint64
	intent cmdsync.ConversationIntent
}

func (r *recordingCMDIntentRemote) PushCMDConversationIntent(_ context.Context, nodeID uint64, intent cmdsync.ConversationIntent) error {
	r.calls = append(r.calls, recordingCMDIntentRemoteCall{nodeID: nodeID, intent: cloneTestCMDConversationIntent(intent)})
	if len(r.errs) == 0 {
		return nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return err
}

type staticUIDOwnerCluster struct {
	uidSlots        map[string]multiraft.SlotID
	leaders         map[multiraft.SlotID][]multiraft.NodeID
	nextSlot        multiraft.SlotID
	lastResolvedUID string
}

func newStaticUIDOwnerCluster(owners map[string]uint64) *staticUIDOwnerCluster {
	cluster := &staticUIDOwnerCluster{uidSlots: make(map[string]multiraft.SlotID), leaders: make(map[multiraft.SlotID][]multiraft.NodeID), nextSlot: 1}
	for uid, owner := range owners {
		cluster.setLeaderSequence(uid, owner)
	}
	return cluster
}

func (c *staticUIDOwnerCluster) setLeaderSequence(uid string, owners ...uint64) {
	slot, ok := c.uidSlots[uid]
	if !ok {
		slot = c.nextSlot
		c.nextSlot++
		c.uidSlots[uid] = slot
	}
	leaders := make([]multiraft.NodeID, len(owners))
	for i, owner := range owners {
		leaders[i] = multiraft.NodeID(owner)
	}
	c.leaders[slot] = leaders
}

func (c *staticUIDOwnerCluster) SlotForKey(uid string) multiraft.SlotID {
	c.lastResolvedUID = uid
	if slot, ok := c.uidSlots[uid]; ok {
		return slot
	}
	c.setLeaderSequence(uid, 0)
	return c.uidSlots[uid]
}

func (c *staticUIDOwnerCluster) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	leaders := c.leaders[slotID]
	if len(leaders) == 0 {
		return 0, nil
	}
	leader := leaders[0]
	if len(leaders) > 1 {
		c.leaders[slotID] = leaders[1:]
	}
	return leader, nil
}

func cloneTestCMDConversationIntent(intent cmdsync.ConversationIntent) cmdsync.ConversationIntent {
	intent.UserReadSeqs = cloneTestReadSeqs(intent.UserReadSeqs)
	return intent
}

func cloneTestReadSeqs(in map[string]uint64) map[string]uint64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(in))
	for uid, readSeq := range in {
		out[uid] = readSeq
	}
	return out
}
