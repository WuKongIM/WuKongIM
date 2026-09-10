package conversation

import (
	"context"
	"encoding/json"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestImportedHiddenMembershipAppearsOnlyAfterNewMessage(t *testing.T) {
	row := metadb.UserChannelMembership{UID: "u1", ChannelID: "g1", ChannelType: 2, JoinSeq: 1}
	require.NoError(t, json.Unmarshal([]byte(`{"ConversationHiddenThroughSeq":9}`), &row))
	directory := &membershipDirectoryStore{rows: []metadb.UserChannelMembership{row}, done: true}
	hydrator := &membershipHeadHydrator{results: []HydrationResult{{Key: ConversationKey{ChannelID: "g1", ChannelType: 2}, Outcome: HydrationOK, LastCommittedSeq: 9, LastMessage: &LastMessage{MessageSeq: 9}}}}
	messages := &recordingLegacyMessageReader{}
	app := New(Options{Directory: directory, Hydrator: hydrator, LegacyMessages: messages})
	result, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.True(t, result.Done)
	legacy, err := app.SyncLegacy(context.Background(), LegacySyncRequest{UID: "u1", MessageCount: 10})
	require.NoError(t, err)
	require.Empty(t, legacy.Items)
	require.Empty(t, messages.queries)
	require.EqualValues(t, 1, hydrator.memberships[0].JoinSeq)
	require.Zero(t, hydrator.memberships[0].DeletedToSeq, "list hiding must not hide history")
	require.Zero(t, hydrator.memberships[0].ReadSeq, "absence must not invent a read position")
	hydrator.results[0].LastCommittedSeq = 10
	hydrator.results[0].LastMessage.MessageSeq = 10
	result, err = app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.EqualValues(t, 10, result.Items[0].LastMessage.MessageSeq)
	require.EqualValues(t, 10, result.Items[0].Unread, "keep native badge math and original default floors")
}
