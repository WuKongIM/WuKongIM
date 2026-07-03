package app

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/legacy/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestPluginCommittedRouterInvokesLocalOwnerOnce(t *testing.T) {
	plugins := &recordingPluginPersistAfterUsecase{}
	router := pluginCommittedRouter{
		localNodeID:   1,
		channelLog:    fakePluginCommittedOwnerResolver{leader: 1},
		pluginUsecase: plugins,
	}
	event := messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 1, MessageSeq: 2}}

	require.NoError(t, router.SubmitCommitted(context.Background(), event))

	require.Len(t, plugins.events, 1)
	require.Equal(t, uint64(1), plugins.events[0].Message.MessageID)
}

func TestPluginCommittedRouterForwardsNonOwnerBeforeInvoking(t *testing.T) {
	plugins := &recordingPluginPersistAfterUsecase{}
	remote := &recordingPluginCommittedSubmitter{}
	router := pluginCommittedRouter{
		localNodeID:   1,
		channelLog:    fakePluginCommittedOwnerResolver{leader: 2},
		nodeClient:    remote,
		pluginUsecase: plugins,
	}
	event := messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 1, MessageSeq: 2}}

	require.NoError(t, router.SubmitCommitted(context.Background(), event))

	require.Empty(t, plugins.events)
	require.Equal(t, []uint64{2}, remote.nodeIDs)
	require.Len(t, remote.events, 1)
	require.Equal(t, uint64(1), remote.events[0].Message.MessageID)
}

func TestPluginCommittedRouterSkipsWhenOwnerUnknown(t *testing.T) {
	plugins := &recordingPluginPersistAfterUsecase{}
	router := pluginCommittedRouter{localNodeID: 1, channelLog: fakePluginCommittedOwnerResolver{}, pluginUsecase: plugins}

	require.NoError(t, router.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageID: 9}}))

	require.Empty(t, plugins.events)
}

func TestPluginCommittedRouterSkipsWhenOwnerLookupFails(t *testing.T) {
	plugins := &recordingPluginPersistAfterUsecase{}
	router := pluginCommittedRouter{localNodeID: 1, channelLog: fakePluginCommittedOwnerResolver{err: errors.New("status failed")}, pluginUsecase: plugins}

	require.NoError(t, router.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageID: 9}}))

	require.Empty(t, plugins.events)
}

func TestPluginCommittedRouterTreatsPersistAfterErrorsAsNonFatal(t *testing.T) {
	plugins := &recordingPluginPersistAfterUsecase{err: errors.New("hook failed")}
	router := pluginCommittedRouter{
		localNodeID:   1,
		channelLog:    fakePluginCommittedOwnerResolver{leader: 1},
		pluginUsecase: plugins,
	}

	err := router.SubmitCommitted(context.Background(), messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageID: 9}})

	require.NoError(t, err)
	require.Len(t, plugins.events, 1)
}

func TestPluginCommittedRouterDetachesCallerCancellation(t *testing.T) {
	plugins := &recordingPluginPersistAfterUsecase{}
	router := pluginCommittedRouter{
		localNodeID:   1,
		channelLog:    fakePluginCommittedOwnerResolver{leader: 1},
		pluginUsecase: plugins,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, router.SubmitCommitted(ctx, messageevents.MessageCommitted{Message: channel.Message{ChannelID: "g1", ChannelType: 2, MessageID: 9}}))

	require.Len(t, plugins.ctxErrs, 1)
	require.NoError(t, plugins.ctxErrs[0])
}

func TestCommittedReplayDoesNotInvokePluginPersistAfter(t *testing.T) {
	key := channelhandler.KeyFromChannelID(channel.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup})
	source := &fakeCommittedReplayLog{
		channels:  []committedReplayChannel{{Key: key, ID: channel.ChannelID{ID: "g1", Type: frame.ChannelTypeGroup}}},
		committed: map[channel.ChannelKey]uint64{key: 1},
		messages:  map[channel.ChannelKey][]channel.Message{key: {{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageID: 1, MessageSeq: 1}}},
	}
	delivery := &fakeCommittedReplayDelivery{}
	conversation := &fakeCommittedReplayConversation{}
	plugins := &recordingPluginPersistAfterUsecase{}
	replayer := newCommittedReplayer(committedReplayerConfig{Log: source, Delivery: delivery, Conversation: conversation})

	require.NoError(t, replayer.RunOnce(context.Background()))

	require.Len(t, delivery.calls, 1)
	require.Len(t, conversation.calls, 1)
	require.Empty(t, plugins.events)
}

func TestNewBuildsPluginCommittedRouterWhenPluginEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Plugin.Enable = true

	app, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, app.Stop()) })

	require.NotNil(t, app.pluginApp)
	dispatcher := messageAppDispatcherForTest(t, app)
	fanout, ok := dispatcher.(committedFanout)
	require.Truef(t, ok, "message dispatcher should be committedFanout, got %T", dispatcher)
	require.Len(t, fanout.subscribers, 3)
	require.Same(t, app.committedDispatcher, fanout.subscribers[0])
	require.Same(t, app.committedReplayer, fanout.subscribers[1])
	router, ok := fanout.subscribers[2].(pluginCommittedRouter)
	require.Truef(t, ok, "third subscriber should be pluginCommittedRouter, got %T", fanout.subscribers[2])
	require.Same(t, app.pluginApp, router.pluginUsecase)

	nodePluginCommitted, ok := unexportedFieldForTest(t, app.nodeAccess, "pluginCommitted").(*pluginusecase.App)
	require.True(t, ok)
	require.Same(t, app.pluginApp, nodePluginCommitted)

	nodeDelivery, ok := unexportedFieldForTest(t, app.nodeAccess, "deliverySubmit").(deliveryRuntimeCommittedSubmitter)
	require.True(t, ok)
	deliveryFanout, ok := nodeDelivery.target.(committedFanout)
	require.Truef(t, ok, "delivery submit target should be committedFanout, got %T", nodeDelivery.target)
	require.Len(t, deliveryFanout.subscribers, 2)
	require.Same(t, app.committedDispatcher, deliveryFanout.subscribers[0])
	require.Same(t, app.committedReplayer, deliveryFanout.subscribers[1])
}

type fakePluginCommittedOwnerResolver struct {
	leader uint64
	err    error
}

func (p fakePluginCommittedOwnerResolver) Status(channel.ChannelID) (channel.ChannelRuntimeStatus, error) {
	return channel.ChannelRuntimeStatus{Leader: channel.NodeID(p.leader)}, p.err
}

type recordingPluginPersistAfterUsecase struct {
	events  []messageevents.MessageCommitted
	ctxErrs []error
	err     error
}

func (r *recordingPluginPersistAfterUsecase) PersistAfterCommitted(ctx context.Context, event messageevents.MessageCommitted) error {
	r.events = append(r.events, event.Clone())
	r.ctxErrs = append(r.ctxErrs, ctx.Err())
	return r.err
}

type recordingPluginCommittedSubmitter struct {
	nodeIDs []uint64
	events  []messageevents.MessageCommitted
	err     error
}

func (r *recordingPluginCommittedSubmitter) SubmitPluginCommitted(_ context.Context, nodeID uint64, event messageevents.MessageCommitted) error {
	r.nodeIDs = append(r.nodeIDs, nodeID)
	r.events = append(r.events, event.Clone())
	return r.err
}

var _ = deliveryruntime.CommittedEnvelope{}
