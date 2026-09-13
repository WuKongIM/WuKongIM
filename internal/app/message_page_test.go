package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

func TestMessagePageCompositionSelectsVisibleSequences(t *testing.T) {
	for _, tc := range []struct {
		name              string
		start, end, floor uint64
		mode              message.PullMode
		want              []uint64
		more              bool
	}{
		{name: "latest_up", floor: 11, mode: message.PullModeUp, want: []uint64{18, 19, 20}, more: true},
		{name: "latest_down", floor: 11, mode: message.PullModeDown, want: []uint64{18, 19, 20}, more: true},
		{name: "forward_range", start: 11, end: 15, floor: 11, mode: message.PullModeUp, want: []uint64{11, 12, 13}, more: true},
		{name: "reverse_range", start: 15, end: 11, floor: 11, mode: message.PullModeDown, want: []uint64{13, 14, 15}, more: true},
		{name: "forward_below_floor", start: 1, floor: 11, mode: message.PullModeUp, want: []uint64{11, 12, 13}, more: true},
		{name: "exhausted_visible_history", floor: 19, mode: message.PullModeUp, want: []uint64{19, 20}},
		{name: "empty_visible_history", floor: 21, mode: message.PullModeUp, want: []uint64{}},
		{name: "reverse_exclusive_end", start: 15, end: 14, floor: 11, mode: message.PullModeDown, want: []uint64{15}},
		{name: "unknown_direction_latest", floor: 11, mode: message.PullMode(2), want: []uint64{18, 19, 20}, more: true},
		{name: "unknown_direction_range", start: 1, end: 12, floor: 11, mode: message.PullMode(2), want: []uint64{11, 12, 13}, more: true},
		{name: "maximum_visibility_floor", floor: ^uint64(0), mode: message.PullModeUp, want: []uint64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, app, _ := newMessagePageComposition(tc.floor)
			query := message.SyncChannelMessagesQuery{LoginUID: "u1", ChannelID: "g1", ChannelType: 2, StartMessageSeq: tc.start, EndMessageSeq: tc.end, Limit: 3, PullMode: tc.mode}
			got, err := app.SyncChannelMessages(context.Background(), query)
			require.NoError(t, err)
			require.Equal(t, tc.want, messagePageSequences(got.Messages))
			require.Equal(t, tc.more, got.More)
			batch, err := app.SyncChannelMessagesBatch(context.Background(), message.SyncChannelMessagesBatchQuery{LoginUID: "u1", Items: []message.SyncChannelMessagesQuery{query}})
			require.NoError(t, err)
			require.Len(t, batch.Items, 1)
			require.NoError(t, batch.Items[0].Err)
			require.Equal(t, got, batch.Items[0].Result)
			require.Len(t, node.reads, 2)
			for _, reads := range node.reads {
				require.Len(t, reads, 1)
				require.Equal(t, 4, reads[0].Request.Limit, "bounded lookahead")
				require.Positive(t, reads[0].Request.MaxBytes)
			}
			require.Zero(t, node.membershipMutationWrites)
		})
	}
}

func TestMessagePageCompositionPreservesLegacyAndPluginContracts(t *testing.T) {
	node, messages, reader := newMessagePageComposition(11)
	store := clusterinfra.NewConversationStore(node)
	conversations := conversationusecase.New(conversationusecase.Options{
		Directory: store, Hydrator: store, LegacyMessages: conversationLegacyMessageReader{messages: messages},
	})
	result, err := conversations.SyncLegacy(context.Background(), conversationusecase.LegacySyncRequest{UID: "u1", MessageCount: 3})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Positive(t, node.persistedReads)
	require.Zero(t, node.committedReads, "legacy recents must not fall back to committed recovery")
	recents := result.Items[0].Recents
	require.Len(t, recents, 3)
	require.Equal(t, []uint64{20, 19, 18}, []uint64{recents[0].MessageSeq, recents[1].MessageSeq, recents[2].MessageSeq})
	recents[0].Payload[0] = 'X'
	require.Equal(t, "message-20", string(node.messages[metadb.ChannelKey{ChannelID: "g1", ChannelType: 2}][19].Payload))

	// Plugin reads deliberately have no ordinary UID membership dependency.
	node.memberships = nil
	before := node.membershipReads
	plugins := newMessagePagePlugin(t, reader)
	response, err := plugins.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{ChannelId: "g1", ChannelType: 2, Limit: 3}}}, "wk.reader")
	require.NoError(t, err)
	require.Equal(t, before, node.membershipReads)
	require.Len(t, response.GetChannelMessageResps(), 1)
	items := response.GetChannelMessageResps()[0].GetMessages()
	require.Len(t, items, 3)
	require.Equal(t, []uint64{18, 19, 20}, []uint64{uint64(items[0].GetMessageSeq()), uint64(items[1].GetMessageSeq()), uint64(items[2].GetMessageSeq())})
	items[0].Payload[0] = 'X'
	require.Equal(t, "message-18", string(node.messages[metadb.ChannelKey{ChannelID: "g1", ChannelType: 2}][17].Payload))
	_, err = messages.SyncChannelMessages(context.Background(), message.SyncChannelMessagesQuery{LoginUID: "u1", ChannelID: "g1", ChannelType: 2})
	require.ErrorIs(t, err, message.ErrSyncMembershipRequired)
}

func TestMessagePageCompositionPreflightsBatchAndPreservesErrorPartitions(t *testing.T) {
	node, app, reader := newMessagePageComposition(11)
	query := message.SyncChannelMessagesQuery{ChannelID: "g1", ChannelType: 2, Limit: 3}
	other := message.SyncChannelMessagesQuery{ChannelID: "g2", ChannelType: 2, Limit: 3}
	batchQuery := message.SyncChannelMessagesBatchQuery{LoginUID: "u1", Items: []message.SyncChannelMessagesQuery{query, other}}
	_, err := app.SyncChannelMessagesBatch(context.Background(), batchQuery)
	require.ErrorIs(t, err, message.ErrSyncMembershipRequired)
	require.Empty(t, node.reads, "no scan before every membership is valid")
	node.memberships[fakeMembershipKey{uid: "u1", channelID: "g2", channelType: 2}] = metadb.UserChannelMembership{UID: "u1", ChannelID: "g2", ChannelType: 2, JoinSeq: 1}
	node.itemErrors["g2"] = channelruntime.ErrNotReady
	batch, err := app.SyncChannelMessagesBatch(context.Background(), batchQuery)
	require.NoError(t, err)
	require.Len(t, batch.Items, 2)
	require.Equal(t, []uint64{18, 19, 20}, messagePageSequences(batch.Items[0].Result.Messages))
	require.ErrorIs(t, batch.Items[1].Err, channelruntime.ErrNotReady)
	require.Len(t, node.reads, 1)
	require.Len(t, node.reads[0], 2)

	// Plugins still read in order and stop at the first non-missing failure.
	plugins := newMessagePagePlugin(t, reader)
	before := len(node.reads)
	_, err = plugins.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{
		{ChannelId: "g2", ChannelType: 2}, {ChannelId: "g1", ChannelType: 2},
	}}, "wk.reader")
	require.ErrorIs(t, err, channelruntime.ErrNotReady)
	require.Len(t, node.reads, before+1)

	node.itemErrors["g2"] = metadb.ErrNotFound
	batch, err = app.SyncChannelMessagesBatch(context.Background(), batchQuery)
	require.NoError(t, err)
	require.NoError(t, batch.Items[1].Err)
	require.Empty(t, batch.Items[1].Result.Messages)
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("scan unavailable")} {
		node.readErr = cause
		query.LoginUID = "u1"
		_, err := app.SyncChannelMessages(context.Background(), query)
		require.ErrorIs(t, err, cause)
		_, err = app.SyncChannelMessagesBatch(context.Background(), batchQuery)
		require.ErrorIs(t, err, cause)
	}
}

func TestMessagePageCompositionEnrichesActualLegacyStreamPage(t *testing.T) {
	node, _, reader := newMessagePageComposition(11)
	node.messages[metadb.ChannelKey{ChannelID: "g1", ChannelType: 2}][19].Setting = 2
	eventKey := message.MessageEventMessageKey{ChannelID: "g1", ChannelType: 2, ClientMsgNo: "client-20"}
	messages := message.New(message.Options{Reader: reader, PersistedReader: message.NewPersistedPageReader(clusterinfra.NewPersistedMessageReader(node)), Memberships: clusterinfra.NewMessageMembershipStore(node), EventStore: legacyConversationEventStore{states: map[message.MessageEventMessageKey][]message.MessageEventState{
		eventKey: {
			{EventKey: message.EventKeyDefault, Status: message.EventStatusClosed, LastMsgEventSeq: 2, SnapshotPayload: []byte(`{"kind":"text","text":"done"}`), EndReason: 3},
			{EventKey: message.EventKeyFinish, Status: message.EventStatusClosed, LastMsgEventSeq: 3},
		},
	}}})
	result, err := (conversationLegacyMessageReader{messages: messages}).ReadLegacyMessagesBatch(context.Background(), "u1", []conversationusecase.LegacyMessageQuery{{ChannelID: "g1", ChannelType: 2, Limit: 3}})
	require.NoError(t, err)
	require.Len(t, result[0].Messages, 3)
	last := result[0].Messages[2]
	require.Equal(t, uint64(20), last.MessageSeq)
	require.Equal(t, uint8(1), last.End)
	require.Equal(t, uint8(3), last.EndReason)
	require.Equal(t, "done", string(last.StreamData))
	require.NotNil(t, last.EventMeta)
	require.True(t, last.EventMeta.Completed)
}

// This fixture provides request-sensitive bounded records, not cluster authority,
// retention or byte-budget validation. All page preparation and construction use
// production implementations; no server, timer or network is started.
type messagePageCluster struct {
	fakePresenceCluster
	reads           [][]clusterchannels.CommittedRead
	persistedReads  int
	committedReads  int
	membershipReads int
	itemErrors      map[string]error
	readErr         error
}

func newMessagePageComposition(floor uint64) (*messagePageCluster, *message.App, *message.PageReader) {
	node := &messagePageCluster{itemErrors: make(map[string]error)}
	node.memberships = map[fakeMembershipKey]metadb.UserChannelMembership{
		{uid: "u1", channelID: "g1", channelType: 2}: {UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 8, DeletedToSeq: floor - 1, ActivatedAt: 100},
	}
	node.messages = make(map[metadb.ChannelKey][]channelruntime.Message)
	key := metadb.ChannelKey{ChannelID: "g1", ChannelType: 2}
	for seq := uint64(1); seq <= 20; seq++ {
		node.messages[key] = append(node.messages[key], channelruntime.Message{
			ChannelID: "g1", ChannelType: 2, MessageID: seq, MessageSeq: seq, FromUID: "u2", ClientMsgNo: fmt.Sprintf("client-%d", seq),
			Payload: []byte(fmt.Sprintf("message-%d", seq)), ServerTimestampMS: 1700000000000 + int64(seq)*1000,
		})
	}
	reader := message.NewPageReader(clusterinfra.NewCommittedMessageReader(node))
	app := message.New(message.Options{Reader: reader, PersistedReader: message.NewPersistedPageReader(clusterinfra.NewPersistedMessageReader(node)), Memberships: clusterinfra.NewMessageMembershipStore(node)})
	return node, app, reader
}

func (n *messagePageCluster) GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error) {
	n.membershipReads++
	return n.fakePresenceCluster.GetUserChannelMembership(ctx, uid, channelID, channelType)
}

func (n *messagePageCluster) ReadChannelCommittedBatch(ctx context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.committedReads++
	return n.readMessages(ctx, reads)
}

func (n *messagePageCluster) readMessages(ctx context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.reads = append(n.reads, append([]clusterchannels.CommittedRead(nil), reads...))
	if n.readErr != nil {
		return nil, n.readErr
	}
	results := make([]clusterchannels.CommittedReadResult, len(reads))
	for index, read := range reads {
		if err := n.itemErrors[read.ChannelID.ID]; err != nil {
			results[index].Err = err
		} else {
			results[index].Read, results[index].Err = n.fakePresenceCluster.ReadChannelCommitted(ctx, read.ChannelID, read.Request)
		}
	}
	return results, nil
}

func messagePageSequences(messages []message.SyncedMessage) []uint64 {
	sequences := make([]uint64, len(messages))
	for index, msg := range messages {
		sequences[index] = msg.MessageSeq
	}
	return sequences
}

// Any unexpected runtime or invoker access panics through the nil embedded port:
// committed plugin reads must not invoke an external plugin process.
type messagePagePluginPorts struct {
	pluginusecase.Runtime
	pluginusecase.Invoker
}

func newMessagePagePlugin(t *testing.T, reader *message.PageReader) *pluginusecase.App {
	t.Helper()
	unused := &messagePagePluginPorts{}
	app, err := pluginusecase.NewApp(pluginusecase.Options{Runtime: unused, Invoker: unused, MessageReader: reader})
	require.NoError(t, err)
	return app
}

func TestMessagePageCompositionFindsHistoryBehindRecoveryBarriers(t *testing.T) {
	node, messages, reader := newMessagePageComposition(1)
	membershipKey := fakeMembershipKey{uid: "u1", channelID: "g1", channelType: 2}
	membership := node.memberships[membershipKey]
	membership.JoinSeq = 1
	node.memberships[membershipKey] = membership
	key := metadb.ChannelKey{ChannelID: "g1", ChannelType: 2}
	for i := range node.messages[key] {
		node.messages[key][i].SyncOnce = i >= 3
	}
	query := message.SyncChannelMessagesQuery{LoginUID: "u1", ChannelID: "g1", ChannelType: 2, Limit: 2}
	got, err := messages.SyncChannelMessages(context.Background(), query)
	require.NoError(t, err)
	require.True(t, got.More)
	require.Equal(t, []uint64{2, 3}, messagePageSequences(got.Messages))
	query.StartMessageSeq = 1
	got, err = messages.SyncChannelMessages(context.Background(), query)
	require.NoError(t, err)
	require.False(t, got.More)
	require.Equal(t, []uint64{1}, messagePageSequences(got.Messages))
	node.memberships = nil
	plugin := newMessagePagePlugin(t, reader)
	gotPlugin, err := plugin.ChannelMessages(context.Background(), &pluginproto.ChannelMessageBatchReq{ChannelMessageReqs: []*pluginproto.ChannelMessageReq{{ChannelId: "g1", ChannelType: 2, Limit: 2}}}, "wk.reader")
	require.NoError(t, err)
	require.Len(t, gotPlugin.ChannelMessageResps, 1)
	rows := gotPlugin.ChannelMessageResps[0].GetMessages()
	require.Len(t, rows, 2)
	require.EqualValues(t, 2, rows[0].MessageSeq)
	require.EqualValues(t, 3, rows[1].MessageSeq)
	require.Zero(t, node.membershipMutationWrites)
}

func (n *messagePageCluster) ReadChannelPersistedBatch(ctx context.Context, reads []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error) {
	n.persistedReads++
	return n.readMessages(ctx, reads)
}

// metadataBatchCompositionNode verifies real adapters discover and use both batch ports.
type metadataBatchCompositionNode struct {
	*messagePageCluster
	membershipBatches int
	channelBatches    int
}

func (n *metadataBatchCompositionNode) GetUserChannelMemberships(_ context.Context, uid string, keys []metadb.ChannelKey) ([]metadb.UserChannelMembership, error) {
	n.membershipBatches++
	rows := make([]metadb.UserChannelMembership, 0, len(keys))
	for _, key := range keys {
		if row, ok := n.memberships[fakeMembershipKey{uid: uid, channelID: key.ChannelID, channelType: key.ChannelType}]; ok {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (n *metadataBatchCompositionNode) ReadPermissionMetadataBatchAuthoritative(_ context.Context, reads []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult {
	n.channelBatches++
	rows := make([]slotproxy.PermissionMetadataReadResult, len(reads))
	for i, r := range reads {
		rows[i] = slotproxy.PermissionMetadataReadResult{Found: true, Channel: metadb.Channel{ChannelID: r.ChannelID, ChannelType: r.ChannelType}}
	}
	return rows
}
func TestConversationSyncCompositionUsesMetadataBatches(t *testing.T) {
	source, _, reader := newMessagePageComposition(11)
	node := &metadataBatchCompositionNode{messagePageCluster: source}
	messages := message.New(message.Options{Reader: reader, PersistedReader: message.NewPersistedPageReader(clusterinfra.NewPersistedMessageReader(node)), Memberships: clusterinfra.NewMessageMembershipStore(node), ChannelState: clusterinfra.NewChannelMetadataStore(metadataBatchCompositionChannels{node: node}, nil, nil)})
	store := clusterinfra.NewConversationStore(node)
	conversations := conversationusecase.New(conversationusecase.Options{Directory: store, Hydrator: store, LegacyMessages: conversationLegacyMessageReader{messages: messages}})
	got, err := conversations.SyncLegacy(context.Background(), conversationusecase.LegacySyncRequest{UID: "u1", MessageCount: 3})
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.Len(t, got.Items[0].Recents, 3)
	require.Equal(t, 1, node.membershipBatches)
	require.Equal(t, 1, node.channelBatches)
	require.Zero(t, node.membershipReads)
	require.Zero(t, node.committedReads)
	require.Positive(t, node.persistedReads)
	require.Zero(t, node.membershipMutationWrites)
}

type metadataBatchCompositionChannels struct {
	clusterinfra.ChannelMetadataNode
	node *metadataBatchCompositionNode
}

func (n metadataBatchCompositionChannels) ReadPermissionMetadataBatchAuthoritative(ctx context.Context, reads []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult {
	return n.node.ReadPermissionMetadataBatchAuthoritative(ctx, reads)
}
