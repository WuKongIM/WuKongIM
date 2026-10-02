package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func mqttProxySession(client string) metadb.MQTTSession {
	return metadb.MQTTSession{Namespace: "main", ClientID: client, UID: "alice", Generation: 1, Revision: 1,
		OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 17, LeaseUntilMS: 5000,
		State: metadb.MQTTSessionActive, SessionExpirySec: 86400, DeviceFlag: 1, ReceiveMaximum: 64, MaxPacketBytes: 1 << 20,
		NextPacketID: 1, NextDeliveryOrder: 1, QuotaMessages: 10000, QuotaBytes: 64 << 20, UpdatedAtMS: 1000}
}

type mqttNoResultCluster struct {
	Cluster
	writes int
}

func (c *mqttNoResultCluster) ProposeWithHashSlot(context.Context, multiraft.SlotID, uint16, []byte) error {
	c.writes++
	return nil
}

type mqttResultCluster struct {
	Cluster
	body []byte
}

func (c *mqttResultCluster) ProposeWithHashSlotResult(context.Context, multiraft.SlotID, uint16, []byte) ([]byte, error) {
	return c.body, nil
}

func TestMQTTWritesRequireExactCommittedResults(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	c := &mqttNoResultCluster{Cluster: nodes[0].cluster}
	s := &Store{cluster: c, db: nodes[0].db}
	_, err := s.CompareAndSwapMQTTSession(context.Background(), 0, mqttProxySession("client"))
	require.Error(t, err)
	require.Zero(t, c.writes, "missing result support must fail before submitting a write")
	for _, body := range []string{"", "null", "{}", `{"Status":0}`, `{"Status":1}`, `{"Status":99,"CurrentRevision":1}`, `{"Status":1,"CurrentRevision":1,"extra":1}`, `{"Status":1,"CurrentRevision":1} {}`, `{"Status":1,"CurrentRevision":2}`} {
		s.cluster = &mqttResultCluster{Cluster: nodes[0].cluster, body: []byte(body)}
		_, err := s.CompareAndSwapMQTTSession(context.Background(), 0, mqttProxySession("client"))
		require.Error(t, err, body)
	}
	for _, body := range []string{metafsm.ApplyResultHashSlotFenced, metafsm.ApplyResultStaleMeta} {
		s.cluster = &mqttResultCluster{Cluster: nodes[0].cluster, body: []byte(body)}
		_, err := s.CompareAndSwapMQTTSession(context.Background(), 0, mqttProxySession("client"))
		require.ErrorIs(t, err, metadb.ErrStaleMeta)
	}
}

func TestMQTTCommittedDecisionFieldsCannotDisappear(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	ctx := context.Background()
	c := &mqttResultCluster{Cluster: nodes[0].cluster}
	s := &Store{cluster: c, db: nodes[0].db}
	m := metadb.MQTTDeliveryCursorMutation{Key: metadb.MQTTDeliveryCursorKey{Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 2, SourceKind: metadb.MQTTSourceChannel, SourceID: "2:group", SourceGeneration: "g"}, ExpectedRevision: 3, OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 17, Op: metadb.MQTTCursorInit, Topic: "topic", UpdatedAtMS: 1000}
	for _, body := range []string{`{"Status":1,"CurrentRevision":4}`, `{"Status":1,"CurrentRevision":4,"SessionState":3}`, `{"Status":1,"CurrentRevision":4,"SessionState":1,"TerminationReason":2}`} {
		c.body = []byte(body)
		_, err := s.MutateMQTTDeliveryCursor(ctx, m)
		require.ErrorIs(t, err, metadb.ErrCorruptValue, body)
	}
	c.body = []byte(`{"Status":1,"CurrentRevision":1,"WillGeneration":2}`)
	_, err := s.ApplyMQTTLifecycle(ctx, metadb.MQTTLifecycleMutation{Event: metadb.MQTTLifecycleConnect, Session: mqttProxySession("client")})
	require.ErrorIs(t, err, metadb.ErrCorruptValue)
}

func TestMQTTSourceAndDetachedWillUseTheirOwnAuthorities(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	s := nodes[0].store
	for _, owner := range []metadb.MQTTBindingOwner{{Kind: metadb.MQTTBindingChannel, ID: "2:group", Generation: "g"}, {Kind: metadb.MQTTBindingUID, ID: "alice"}} {
		r := metadb.MQTTSourceBinding{Key: metadb.MQTTSourceBindingKey{Owner: owner, Namespace: "main", ClientID: "client", SessionGeneration: 1, SubscriptionGeneration: 2}, UID: "alice", Topic: "topic", Revision: 1, IntentRevision: 2, AuthorizationVersion: 1, OperationID: "subscribe", Stage: metadb.MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}
		applied, err := s.CompareAndSwapMQTTSourceBinding(ctx, 0, r)
		require.NoError(t, err)
		require.Equal(t, metadb.MQTTSessionCASApplied, applied.Status)
		got, err := s.ReadMQTT(ctx, metadb.MQTTRead{Kind: metadb.MQTTReadSourceCandidates, Owner: owner, Limit: 1})
		require.NoError(t, err)
		require.Equal(t, []metadb.MQTTSourceBinding{r}, got.Bindings)
		require.Nil(t, got.Session)
	}
	k := metadb.MQTTWillKey{Namespace: "main", ClientID: "detached", SessionGeneration: 1, WillGeneration: 2}
	id, err := metadb.MQTTWillIdempotencyKey(k)
	require.NoError(t, err)
	w := metadb.MQTTWill{Key: k, UID: "alice", OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 17, Revision: 1, DecisionRevision: 2, Topic: "topic", TargetID: "group", TargetType: 2, Payload: []byte("gone"), PublicationMetadata: []byte{1}, QoS: 1, ClientMsgNo: "will", IdempotencyKey: id, Stage: metadb.MQTTWillArmed, UpdatedAtMS: 1000}
	r, err := s.CompareAndSwapMQTTWill(ctx, 0, w)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, r.Status)
	got, err := s.ReadMQTT(ctx, metadb.MQTTRead{Kind: metadb.MQTTReadWill, WillKey: k})
	require.NoError(t, err)
	require.Nil(t, got.Session)
	require.Equal(t, []metadb.MQTTWill{w}, got.Wills)
	got.Wills[0].Payload[0] = 'x'
	got, err = s.ReadMQTT(ctx, metadb.MQTTRead{Kind: metadb.MQTTReadWill, WillKey: k})
	require.NoError(t, err)
	require.Equal(t, []byte("gone"), got.Wills[0].Payload)
}

func TestMQTTSlotRoutesAtomicSessionChildrenAndFreshReads(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	store := nodes[0].store
	client := ""
	for i := 0; i < 10000; i++ {
		candidate := fmt.Sprintf("mqtt-%d", i)
		key, err := MQTTSessionRoutingKey("main", candidate)
		require.NoError(t, err)
		if store.cluster.SlotForKey(key) == 2 && store.cluster.HashSlotForKey(key) != 2 {
			client = candidate
			break
		}
	}
	require.NotEmpty(t, client)
	session := mqttProxySession(client)
	result, err := store.ApplyMQTTLifecycle(ctx, metadb.MQTTLifecycleMutation{Event: metadb.MQTTLifecycleConnect, Session: session})
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, result.Status)
	key, _ := MQTTSessionRoutingKey(session.Namespace, client)
	hs := store.cluster.HashSlotForKey(key)
	_, found, err := nodes[0].db.ForHashSlot(hs).GetMQTTSession(ctx, "main", client)
	require.NoError(t, err)
	require.False(t, found, "origin replica intentionally has no Session row")
	for range 2 {
		before := nodes[1].cluster.nextIndex[2]
		read, err := store.ReadMQTT(ctx, metadb.MQTTRead{Kind: metadb.MQTTReadSession, Namespace: "main", ClientID: client})
		require.NoError(t, err)
		require.Equal(t, uint64(1), read.Session.Revision)
		require.Equal(t, before+1, nodes[1].cluster.nextIndex[2], "each read needs a fresh applied barrier")
	}
	sub := metadb.MQTTSubscription{Namespace: "main", ClientID: client, SessionGeneration: 1, Topic: "topic", Generation: 2, Revision: 2,
		TargetKind: metadb.MQTTSubscriptionGroup, TargetID: "group", GrantedQoS: 1, AuthorizationVersion: 1,
		Stage: metadb.MQTTSubscriptionPreparing, OperationID: "subscribe", RecoveryAtMS: 1000, UpdatedAtMS: 1000}
	m := metadb.MQTTSubscriptionMutation{ExpectedRevision: 1, OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 17, Subscription: sub}
	stale := m
	stale.OwnerBootID = "old"
	bad, err := store.MutateMQTTSubscription(ctx, stale)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASConflict, bad.Status)
	applied, err := store.MutateMQTTSubscription(ctx, m)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, applied.Status)
	m.ExpectedRevision, m.Subscription.Revision = 2, 3
	m.Subscription.Stage, m.Subscription.RecoveryAtMS = metadb.MQTTSubscriptionActive, 0
	applied, err = store.MutateMQTTSubscription(ctx, m)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, applied.Status)
	k := metadb.MQTTDeliveryCursorKey{Namespace: "main", ClientID: client, SessionGeneration: 1, SubscriptionGeneration: 2, SourceKind: metadb.MQTTSourceChannel, SourceID: "2:group", SourceGeneration: "g"}
	cursor := metadb.MQTTDeliveryCursorMutation{Key: k, ExpectedRevision: 3, OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 17, Op: metadb.MQTTCursorInit, Topic: "topic", AuthorizationVersion: 1, UpdatedAtMS: 1000}
	counted, err := store.MutateMQTTDeliveryCursor(ctx, cursor)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, counted.Status)
	cursor.Op, cursor.ExpectedRevision, cursor.Through, cursor.AddedMessages, cursor.AddedBytes = metadb.MQTTCursorAccount, 4, 1, 1, 3
	counted, err = store.MutateMQTTDeliveryCursor(ctx, cursor)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTSessionCASApplied, counted.Status)
	window := metadb.MQTTWindowMutation{Key: k, ExpectedRevision: 5, OwnerGeneration: 1, OwnerNodeID: 1, OwnerBootID: "boot", ConnectionID: 17, Op: metadb.MQTTWindowAdmit,
		Publication: metadb.MQTTInflightPublication{Position: 1, MessageID: 41, MessageSeq: 1, ContentVersion: 1, ContentHash: strings.Repeat("a", 64), Bytes: 3}, UpdatedAtMS: 1000}
	exchange, err := store.MutateMQTTWindow(ctx, window)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTWindowApplied, exchange.Status)
	read, err := store.ReadMQTT(ctx, metadb.MQTTRead{Kind: metadb.MQTTReadInflightPage, Namespace: "main", ClientID: client, SessionGeneration: 1, Limit: 1})
	require.NoError(t, err)
	require.Len(t, read.Inflight, 1)
	require.Equal(t, uint16(1), read.Session.OutboundInflight)
	require.Equal(t, exchange.PacketID, read.Inflight[0].PacketID)
	window.Op, window.ExpectedRevision, window.PacketID, window.DeliveryOrder = metadb.MQTTWindowAck, 6, exchange.PacketID, exchange.DeliveryOrder
	window.Publication = metadb.MQTTInflightPublication{}
	exchange, err = store.MutateMQTTWindow(ctx, window)
	require.NoError(t, err)
	require.Equal(t, metadb.MQTTWindowApplied, exchange.Status)
	recovery, err := store.ReadMQTTRecovery(ctx, hs, metadb.MQTTRead{Kind: metadb.MQTTReadSessionDeadlines, Limit: 1})
	require.NoError(t, err)
	require.Len(t, recovery.Sessions, 1)
	require.Equal(t, uint64(7), recovery.Sessions[0].Revision)
}

func TestMQTTRoutingIdentityAndSourceOwnership(t *testing.T) {
	a, err := MQTTSessionRoutingKey("a:b", "c")
	require.NoError(t, err)
	b, err := MQTTSessionRoutingKey("a", "b:c")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	for _, owner := range []metadb.MQTTBindingOwner{{Kind: metadb.MQTTBindingChannel, ID: "2:room:west", Generation: "g1"}, {Kind: metadb.MQTTBindingChannel, ID: "2:room:west", Generation: "g2"}} {
		key, err := MQTTSourceRoutingKey(owner)
		require.NoError(t, err)
		require.Equal(t, "room:west", key)
	}
	key, err := MQTTSourceRoutingKey(metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingUID, ID: "alice"})
	require.NoError(t, err)
	require.Equal(t, "alice", key)
	for _, id := range []string{"room", "02:room", "0:room", "256:room", "2:"} {
		_, err := MQTTSourceRoutingKey(metadb.MQTTBindingOwner{Kind: metadb.MQTTBindingChannel, ID: id, Generation: "g"})
		require.Error(t, err)
	}
}

func TestMQTTReadRPCRejectsRouteChangesAndMalformedWork(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 256)
	c := &changingReadAuthority{proxyTestCluster: nodes[1].cluster}
	store := NewChannelMetadataStore(c, nodes[1].db)
	_, err := store.ReadMQTTRecovery(context.Background(), c.HashSlotsOf(2)[0], metadb.MQTTRead{Kind: metadb.MQTTReadSessionDeadlines, Limit: 1})
	require.ErrorIs(t, err, ErrReadStaleRoute)
	for _, body := range [][]byte{[]byte(`{"format":99,"probe":true}`), []byte(`{"format":1,"probe":true,"unknown":1}`), []byte(`{"format":1,"probe":true} {}`), []byte(strings.Repeat(" ", (64<<10)+1))} {
		_, err := store.handleMQTTReadRPC(context.Background(), body)
		require.Error(t, err)
	}
	q := mqttReadRPC{Format: 1, SlotID: 2, HashSlot: nodes[1].cluster.HashSlotsOf(1)[0], Query: metadb.MQTTRead{Kind: metadb.MQTTReadSessionDeadlines, Limit: 1}}
	body, err := json.Marshal(q)
	require.NoError(t, err)
	_, err = nodes[1].store.handleMQTTReadRPC(context.Background(), body)
	require.ErrorIs(t, err, ErrReadStaleRoute)
}

func TestMQTTReadReplyRequiresExactQueryAndBoundedShape(t *testing.T) {
	q := mqttReadRPC{Format: 1, SlotID: 2, HashSlot: 17, Query: metadb.MQTTRead{Kind: metadb.MQTTReadSession, Namespace: "main", ClientID: "client"}}
	session := mqttProxySession("client")
	good := mqttReadReply{Format: 1, SlotID: 2, HashSlot: 17, Query: q.Query, Status: rpcStatusOK, Result: &metadb.MQTTReadResult{Session: &session, Done: true}}
	body, err := json.Marshal(good)
	require.NoError(t, err)
	_, err = decodeMQTTReadReply(body, q)
	require.NoError(t, err)
	for _, change := range []func(*mqttReadReply){
		func(r *mqttReadReply) { r.Format = 0 }, func(r *mqttReadReply) { r.SlotID++ }, func(r *mqttReadReply) { r.HashSlot++ },
		func(r *mqttReadReply) { r.Query.ClientID = "other" }, func(r *mqttReadReply) { r.Result = nil },
		func(r *mqttReadReply) { r.Result.Done = false },
		func(r *mqttReadReply) { bad := session; bad.ClientID = "other"; r.Result.Session = &bad },
		func(r *mqttReadReply) { r.Result.Sessions = []metadb.MQTTSession{session} },
	} {
		r := good
		v := *good.Result
		r.Result = &v
		change(&r)
		body, err := json.Marshal(r)
		require.NoError(t, err)
		_, err = decodeMQTTReadReply(body, q)
		require.Error(t, err)
	}
}
