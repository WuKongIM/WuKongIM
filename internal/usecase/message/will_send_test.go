package message

import (
	"context"
	"strings"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestWillPreparationFreezesOnlyPayloadAndPreparedSendRechecksPolicy(t *testing.T) {
	ctx := context.Background()
	store := newFakePermissionStore()
	store.channels[permissionKey("group", 2)] = metadb.Channel{ChannelID: "group", ChannelType: 2}
	store.members[permissionKey("group", 2)] = map[string]bool{"alice": true}
	md, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "topic", ServerWillKey: "mqtt-will-v1:" + strings.Repeat("a", 64)})
	require.NoError(t, err)
	q := WillSendCommand{FromUID: "alice", TargetID: "group", TargetType: 2, ClientMsgNo: "will", Payload: []byte("original"), PublicationMetadata: md}
	steps := 0
	hook := &recordingSendHook{mutate: func(c SendCommand) (SendCommand, Reason, error) {
		steps++
		c.Payload = []byte("plugin")
		return c, ReasonSuccess, nil
	}}
	webhook := testBeforeSendWebhook(t, beforeSendCallerFunc(func(_ context.Context, r BeforeSendRequest) (BeforeSendDecision, error) {
		steps++
		require.Equal(t, "plugin", string(r.Payload))
		return BeforeSendDecision{Allow: true, Payload: []byte("frozen")}, nil
	}), nil)
	submitter := &recordingSubmitter{sendResult: SendResult{Reason: ReasonSuccess, MessageID: 99, MessageSeq: 7}}
	a := New(Options{PermissionStore: store, PermissionCacheTTL: time.Hour, Submitter: submitter, SendHook: hook, BeforeSendWebhook: webhook})
	body, reason, err := a.PrepareWill(ctx, q)
	require.NoError(t, err)
	require.Equal(t, ReasonSuccess, reason)
	require.Equal(t, "frozen", string(body))
	require.Nil(t, submitter.sendCtx)
	q.Payload = body
	r, err := a.SendPreparedWill(ctx, q)
	require.NoError(t, err)
	require.Equal(t, ReasonSuccess, r.Reason)
	require.Equal(t, "frozen", string(submitter.sendCommand.Payload))
	require.Equal(t, md, submitter.sendCommand.PublicationMetadata)
	require.Equal(t, 2, steps, "dispatch must not run either transformation again")
	delete(store.members[permissionKey("group", 2)], "alice")
	r, err = a.SendPreparedWill(ctx, q)
	require.NoError(t, err)
	require.Equal(t, ReasonSubscriberNotExist, r.Reason)
	require.Equal(t, 2, steps)
	store.members[permissionKey("group", 2)]["alice"] = true
	hook.mutate = func(c SendCommand) (SendCommand, Reason, error) { c.FromUID = "system"; return c, ReasonSuccess, nil }
	_, reason, err = a.PrepareWill(ctx, q)
	require.Error(t, err)
	require.Equal(t, ReasonInvalidRequest, reason)
	q.PublicationMetadata = nil
	_, _, err = a.PrepareWill(ctx, q)
	require.Error(t, err)
	_, err = a.SendPreparedWill(ctx, q)
	require.Error(t, err)
}
