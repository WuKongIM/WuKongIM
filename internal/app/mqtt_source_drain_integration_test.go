//go:build integration

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type mqttDrainInterruptedPreparation struct {
	*cluster.Node
	binding      meta.MQTTSourceBinding
	afterBinding func(context.Context, meta.MQTTSourceBinding) error
}

func (s *mqttDrainInterruptedPreparation) CompareAndSwapMQTTSourceBinding(ctx context.Context, rev uint64, b meta.MQTTSourceBinding) (meta.MQTTSourceBindingResult, error) {
	r, err := s.Node.CompareAndSwapMQTTSourceBinding(ctx, rev, b)
	if err == nil && r.Status == meta.MQTTSessionCASApplied && s.afterBinding != nil {
		if err = s.afterBinding(ctx, b); err != nil {
			return meta.MQTTSourceBindingResult{}, err
		}
	}
	if err == nil && r.Status == meta.MQTTSessionCASApplied && !b.BoundaryKnown {
		s.binding = b
		return meta.MQTTSourceBindingResult{}, context.DeadlineExceeded
	}
	return r, err
}

// verifyMQTTSourceDrain composes real unsubscribe, Slot sealing/window commits
// and cancellation initialization. Establishment/window content proof remains a
// controlled fixture until the complete delivery projection is composed.
func verifyMQTTSourceDrain(t *testing.T, ctx context.Context, nodes []*cluster.Node, owners []*runtime.Owners, sessions []*sessioncase.App, authorization sessioncase.SubscriptionAuthorizer, protector sessioncase.SourceProtector) {
	t.Helper()
	for _, mode := range []string{"backlog", "cancel"} {
		connection, err := sessions[0].Connect(ctx, sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "drain-" + mode}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }})
		require.NoError(t, err)
		o := connection.Owner
		projection := &mqttSubscriptionProjectionFixture{establish: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
			return sessioncase.SubscriptionProjectionReceipt{}, sessioncase.ErrEvidence
		}}
		subs, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[0], Owners: owners[0], Authorization: authorization, Projection: projection})
		require.NoError(t, err)
		request := sessioncase.SubscriptionRequest{Topic: "wk/v1/groups/Z3JvdXA/messages", TargetKind: meta.MQTTSubscriptionGroup, TargetID: "group", RequestedQoS: 1}
		_, err = subs.Subscribe(ctx, o, request)
		require.ErrorIs(t, err, sessioncase.ErrEvidence)
		interrupted := &mqttDrainInterruptedPreparation{Node: nodes[0]}
		var metadata sessioncase.GroupSourceMetadata = nodes[0]
		if mode == "cancel" {
			metadata = interrupted
		}
		sources, err := sessioncase.NewGroupSources(sessioncase.GroupSourceOptions{Store: metadata, Owners: owners[0], Authorization: authorization, Sources: protector})
		require.NoError(t, err)
		prepared, err := sources.Prepare(ctx, o, request.Topic)
		var key meta.MQTTSourceBindingKey
		var before []meta.MQTTInflight
		if mode == "cancel" {
			require.ErrorIs(t, err, context.DeadlineExceeded)
			key = interrupted.binding.Key
		} else {
			require.NoError(t, err)
			key = prepared.Binding.Key
			projection.establish = func(_ context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
				return mqttSubscriptionFixtureReceipt(r), nil
			}
			_, err = subs.Subscribe(ctx, o, request)
			require.NoError(t, err)
			r, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: prepared.Cursor.Key})
			require.NoError(t, err)
			account, err := nodes[1].MutateMQTTDeliveryCursor(ctx, meta.MQTTDeliveryCursorMutation{Key: prepared.Cursor.Key, ExpectedRevision: r.Session.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTCursorAccount, Topic: prepared.Cursor.Topic, AuthorizationVersion: prepared.Cursor.AuthorizationVersion, Through: prepared.Cursor.StartAfter + 3, AddedMessages: 3, AddedBytes: 3, UpdatedAtMS: time.Now().UnixMilli()})
			require.NoError(t, err)
			require.Equal(t, meta.MQTTSessionCASApplied, account.Status)
			for i := uint64(1); i <= 2; i++ {
				admitted, err := nodes[2].MutateMQTTWindow(ctx, meta.MQTTWindowMutation{Key: prepared.Cursor.Key, ExpectedRevision: account.CurrentRevision + i - 1, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAdmit, Publication: meta.MQTTInflightPublication{Position: prepared.Cursor.StartAfter + i, MessageID: 30000 + i, MessageSeq: prepared.Cursor.StartAfter + i, ContentVersion: 1, ContentHash: strings.Repeat("a", 64), Bytes: 1}, UpdatedAtMS: time.Now().UnixMilli()})
				require.NoError(t, err)
				require.Equal(t, meta.MQTTWindowApplied, admitted.Status)
			}
			exchanges, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Limit: 16})
			require.NoError(t, err)
			before = exchanges.Inflight
			require.Len(t, before, 2)
		}
		drain, err := newMQTTSourceDrain(nodes[0], owners[0], protector)
		require.NoError(t, err)
		if mode == "backlog" {
			// An ACK commits after the source seal but before the Session release.
			// The stale window CAS must fail without subtracting unadmitted quota.
			last := before[1]
			interrupted.afterBinding = func(c context.Context, b meta.MQTTSourceBinding) error {
				if !b.EndKnown {
					return sessioncase.ErrEvidence
				}
				view, e := nodes[1].ReadMQTT(c, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: prepared.Cursor.Key})
				if e != nil {
					return e
				}
				ack, e := nodes[2].MutateMQTTWindow(c, meta.MQTTWindowMutation{Key: prepared.Cursor.Key, ExpectedRevision: view.Session.Revision, OwnerGeneration: o.OwnerGeneration, OwnerNodeID: o.NodeID, OwnerBootID: o.BootID, ConnectionID: o.ConnectionID, Op: meta.MQTTWindowAck, PacketID: last.PacketID, DeliveryOrder: last.DeliveryOrder, UpdatedAtMS: time.Now().UnixMilli()})
				if e == nil && ack.Status != meta.MQTTWindowApplied {
					e = sessioncase.ErrEvidence
				}
				return e
			}
			racing, e := sessioncase.NewSourceDrain(sessioncase.SourceDrainOptions{Store: interrupted, Owners: owners[0], Sources: protector})
			require.NoError(t, e)
			projection.remove = func(c context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
				_, e := racing.Seal(c, r.Owner, key)
				return mqttSubscriptionFixtureReceipt(r), e
			}
			_, err = subs.Unsubscribe(ctx, o, request.Topic)
			require.ErrorIs(t, err, sessioncase.ErrConflict)
			interrupted.afterBinding = nil
			exchanges, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Limit: 16})
			require.NoError(t, err)
			require.EqualValues(t, 2, exchanges.Session.PendingMessages)
			require.Len(t, exchanges.Inflight, 1)
			require.Equal(t, before[0].Publication, exchanges.Inflight[0].Publication)
			require.Equal(t, before[0].PacketID, exchanges.Inflight[0].PacketID)
			before = exchanges.Inflight
		}
		var sealed sessioncase.SourceDrainResult
		projection.remove = func(c context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
			var e error
			sealed, e = drain.Seal(c, r.Owner, key)
			return mqttSubscriptionFixtureReceipt(r), e
		}
		_, err = subs.Unsubscribe(ctx, o, request.Topic)
		require.NoError(t, err)
		r, err := nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: sealed.Cursor.Key})
		require.NoError(t, err)
		require.Equal(t, []meta.MQTTDeliveryCursor{sealed.Cursor}, r.DeliveryCursors)
		require.Equal(t, uint64(sealed.Cursor.InflightCount), r.Session.PendingMessages)
		require.Equal(t, sealed.Binding.EndThrough, sealed.Cursor.WindowThrough)
		if mode == "backlog" {
			require.EqualValues(t, 1, r.Session.PendingMessages)
			exchanges, err := nodes[2].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInflightPage, Namespace: o.Key.Namespace, ClientID: o.Key.ClientID, SessionGeneration: o.SessionGeneration, Limit: 16})
			require.NoError(t, err)
			require.Equal(t, before, exchanges.Inflight)
		} else {
			require.Zero(t, r.Session.PendingMessages)
			require.Positive(t, sealed.Binding.ProgressRevision)
		}
		t.Logf("mqtt_source_drain_evidence: nodes=3 hash_slots=256 mode=%s unsubscribe_real=true sealed_end=true remote_cursor_read=true concurrent_ack_cas=%t quota_preserved=true inflight_preserved=true establishment_and_window_admission=controlled product_listener=false", mode, mode == "backlog")
	}
}
