//go:build integration

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

// loseWillObservation loses the caller's publication observation while the real
// message usecase has already committed. It does not fabricate a storage receipt.
type loseWillObservation struct{ sessioncase.WillPublications }

func (p loseWillObservation) PublishWill(ctx context.Context, q sessioncase.WillPublication) error {
	if err := p.WillPublications.PublishWill(ctx, q); err != nil {
		return err
	}
	return errors.New("injected lost append reply")
}
func (p loseWillObservation) LookupWillPublication(context.Context, sessioncase.WillPublication) (sessioncase.WillPublicationReceipt, bool, error) {
	return sessioncase.WillPublicationReceipt{}, false, errors.New("injected unavailable observation")
}

// Real Session lifecycle, foreground Slot CAS, SEND and routed retained receipts
// run on the production app's 256-hash-slot single-node cluster. No MQTT listener
// is opened; this is composition coverage, not process-level product acceptance.
func TestMQTTWillExecutionSingleNodeClusterRecoversBeforeRevocation(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	id := ch.ChannelID{ID: "will-execution", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, id.ID, cfg.NodeID)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	seedGroupSendPermission(t, node, id, "alice")
	now := time.Now()
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: cfg.NodeID, BootID: "will-owner", Capacity: 4, MaxOperations: 4, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second, Now: func() time.Time { return now }})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owners.Close(context.Background())) })
	require.NoError(t, node.UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, Token: "secret", DeviceLevel: 1}))
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: owners, Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: node}}), Wills: mqttWillAuthorizer{messages: a.Messages()}, Now: func() time.Time { return now }, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
	require.NoError(t, err)
	topic, err := access.FormatTopic(access.Target{ChannelID: id.ID, ChannelType: id.Type})
	require.NoError(t, err)
	ready := func(client string) meta.MQTTWillKey {
		md, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "main", PublisherClientID: client, OriginalTopic: topic, Properties: []publication.Property{{Kind: publication.MessageExpiry, Number: 60}}})
		require.NoError(t, err)
		conn, err := sessions.Connect(ctx, sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: client}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }, Will: &sessioncase.Will{WillTarget: sessioncase.WillTarget{Topic: topic, TargetID: id.ID, TargetType: id.Type}, QoS: 1, ClientMsgNo: "same-client-number", Payload: []byte(client), PublicationMetadata: md}})
		require.NoError(t, err)
		require.NoError(t, sessions.Disconnect(ctx, sessioncase.DisconnectCommand{Owner: conn.Owner}))
		return meta.MQTTWillKey{Namespace: "main", ClientID: client, SessionGeneration: conn.Owner.SessionGeneration, WillGeneration: conn.WillGeneration}
	}
	first, lost, denied := ready("first"), ready("lost"), ready("denied")
	opts := sessioncase.WillExecutionOptions{NodeID: cfg.NodeID, BootID: "executor-1", Now: func() time.Time { return now }, LeaseDuration: 10 * time.Second, TurnTimeout: 5 * time.Second}
	e, err := newMQTTWillExecutor(node, a.Messages(), opts)
	require.NoError(t, err)
	r, err := e.Execute(ctx, first)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTWillPublished, r.Stage)
	require.Equal(t, uint64(1), r.Receipt.MessageSeq)
	reader, err := clusterinfra.NewMQTTWillReceipts(channels.NewSlotMetaSource(node), node)
	require.NoError(t, err)
	opts.Store, opts.Authorizer = node, mqttWillAuthorizer{messages: a.Messages()}
	opts.Publications = loseWillObservation{WillPublications: mqttWillPublications{MQTTWillReceipts: reader, messages: a.Messages()}}
	interrupted, err := sessioncase.NewWillExecutor(opts)
	require.NoError(t, err)
	_, err = interrupted.Execute(ctx, lost)
	require.Error(t, err)
	before, err := node.ReadChannelOriginalCommittedBatch(ctx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 4096}}})
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.NoError(t, before[0].Err)
	require.Len(t, before[0].Read.Messages, 2, "lost reply must follow a real commit")
	original := before[0].Read.Messages[1]
	require.NoError(t, node.RemoveChannelSubscribers(ctx, id.ID, int64(id.Type), []string{"alice"}, 2))
	now = now.Add(11 * time.Second)
	opts.BootID = "executor-2"
	recovered, err := newMQTTWillExecutor(node, a.Messages(), opts)
	require.NoError(t, err)
	r, err = recovered.Execute(ctx, lost)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTWillPublished, r.Stage)
	require.Equal(t, sessioncase.WillPublicationReceipt{MessageID: original.MessageID, MessageSeq: original.MessageSeq, PublishedAtMS: original.ServerTimestampMS}, r.Receipt)
	r, err = recovered.Execute(ctx, denied)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTWillRejected, r.Stage)
	after, err := node.ReadChannelOriginalCommittedBatch(ctx, []channels.CommittedRead{{ChannelID: id, Request: store.ReadCommittedRequest{FromSeq: 1, Limit: 10, MaxBytes: 4096}}})
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.NoError(t, after[0].Err)
	require.Equal(t, before[0].Read.Messages, after[0].Read.Messages, "recovery and denial append no business message")
}
