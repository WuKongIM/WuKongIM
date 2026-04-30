package app

import (
	"context"
	"runtime"
	"strconv"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestAsyncCommittedDispatcherBurstKeepsGoroutinesBounded(t *testing.T) {
	delivery := newBlockingCommittedSubmitter()
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		PreferLocal: true,
		Delivery:    delivery,
		ShardCount:  4,
		QueueDepth:  16,
	})
	require.NoError(t, dispatcher.Start(context.Background()))
	defer func() {
		delivery.Release()
		require.NoError(t, dispatcher.Stop())
	}()

	baseline := runtime.NumGoroutine()
	for i := 0; i < 256; i++ {
		require.NoError(t, dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
			Message: channel.Message{
				ChannelID:   "burst",
				ChannelType: frame.ChannelTypeGroup,
				MessageID:   uint64(i + 1),
				MessageSeq:  uint64(i + 1),
			},
		}))
	}
	delivery.WaitEntered(t)

	require.LessOrEqual(t, runtime.NumGoroutine()-baseline, 8)
}

func BenchmarkBuildRealtimeRecvPacketPersonChannelView(b *testing.B) {
	msg := benchmarkDeliveryMessage(frame.ChannelTypePerson, "u1@u2")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		recipient := "u2"
		if i%2 == 0 {
			recipient = "u1"
		}
		if packet := buildRealtimeRecvPacket(msg, recipient); packet.ChannelID == "" {
			b.Fatal("empty recipient channel view")
		}
	}
}

func BenchmarkLocalDeliveryPushPersonRoutes(b *testing.B) {
	const routeCount = 256
	registry := online.NewRegistry()
	routes := make([]deliveryruntime.RouteKey, 0, routeCount)
	for i := 0; i < routeCount; i++ {
		uid := "u1"
		if i%2 == 1 {
			uid = "u2"
		}
		sessionID := uint64(i + 1)
		routes = append(routes, deliveryruntime.RouteKey{UID: uid, NodeID: 1, BootID: 11, SessionID: sessionID})
		if err := registry.Register(online.OnlineConn{
			SessionID: sessionID,
			UID:       uid,
			State:     online.LocalRouteStateActive,
			Session:   benchmarkSession{id: sessionID},
		}); err != nil {
			b.Fatalf("register route: %v", err)
		}
	}
	push := localDeliveryPush{online: registry, localNodeID: 1, gatewayBootID: 11}
	cmd := deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{Message: benchmarkDeliveryMessage(frame.ChannelTypePerson, "u1@u2")},
		Routes:   routes,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := push.Push(context.Background(), cmd)
		if err != nil {
			b.Fatalf("push local delivery: %v", err)
		}
		if len(result.Accepted) != routeCount {
			b.Fatalf("accepted routes = %d, want %d", len(result.Accepted), routeCount)
		}
	}
}

func BenchmarkDistributedDeliveryPushGroupBatchRoutes(b *testing.B) {
	const routeCount = 256
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      benchmarkAcceptAllDeliveryPushClient{},
		codec:       codec.New(),
	}
	cmd := deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{Message: benchmarkDeliveryMessage(frame.ChannelTypeGroup, "g1")},
		Routes:   benchmarkRemoteRoutes(routeCount, 2, "u"),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := push.Push(context.Background(), cmd)
		if err != nil {
			b.Fatalf("push distributed delivery: %v", err)
		}
		if len(result.Accepted) != routeCount {
			b.Fatalf("accepted routes = %d, want %d", len(result.Accepted), routeCount)
		}
	}
}

func BenchmarkDistributedDeliveryPushPersonRouteViews(b *testing.B) {
	const routeCount = 256
	push := distributedDeliveryPush{
		localNodeID: 1,
		client:      benchmarkAcceptAllDeliveryPushClient{},
		codec:       codec.New(),
	}
	cmd := deliveryruntime.PushCommand{
		Envelope: deliveryruntime.CommittedEnvelope{Message: benchmarkDeliveryMessage(frame.ChannelTypePerson, "u1@u2")},
		Routes:   benchmarkPersonRemoteRoutes(routeCount, 2),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := push.Push(context.Background(), cmd)
		if err != nil {
			b.Fatalf("push distributed delivery: %v", err)
		}
		if len(result.Accepted) != routeCount {
			b.Fatalf("accepted routes = %d, want %d", len(result.Accepted), routeCount)
		}
	}
}

func BenchmarkAsyncCommittedDispatcherSubmitCommitted(b *testing.B) {
	dispatcher := newAsyncCommittedDispatcher(asyncCommittedDispatcherConfig{
		LocalNodeID: 1,
		PreferLocal: true,
		Delivery:    benchmarkCommittedDeliverySubmitter{},
		ShardCount:  4,
		QueueDepth:  1024,
	})
	if err := dispatcher.Start(context.Background()); err != nil {
		b.Fatalf("start committed dispatcher: %v", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = dispatcher.StopContext(context.Background())
		}
	}()

	event := messageevents.MessageCommitted{Message: benchmarkDeliveryMessage(frame.ChannelTypeGroup, "g-dispatch")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event.Message.MessageID = uint64(i + 1)
		event.Message.MessageSeq = uint64(i + 1)
		if err := dispatcher.SubmitCommitted(context.Background(), event); err != nil {
			b.Fatalf("submit committed message: %v", err)
		}
	}
	b.StopTimer()
	if err := dispatcher.StopContext(context.Background()); err != nil {
		b.Fatalf("stop committed dispatcher: %v", err)
	}
	stopped = true
}

func BenchmarkLocalDeliveryResolverResolvePagePersonChannel(b *testing.B) {
	resolver := localDeliveryResolver{
		subscribers: deliveryusecase.NewSubscriberResolver(deliveryusecase.SubscriberResolverOptions{}),
		authority: benchmarkAuthoritative{
			routes: map[string][]presence.Route{
				"u2": {{UID: "u2", NodeID: 1, BootID: 11, SessionID: 2}},
			},
		},
		pageSize: 256,
	}
	key := deliveryruntime.ChannelKey{
		ChannelID:   deliveryusecase.EncodePersonChannel("u1", "u2"),
		ChannelType: frame.ChannelTypePerson,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		token, err := resolver.BeginResolve(context.Background(), key, deliveryruntime.CommittedEnvelope{})
		if err != nil {
			b.Fatalf("begin resolve: %v", err)
		}
		routes, _, done, err := resolver.ResolvePage(context.Background(), token, "", 256)
		if err != nil {
			b.Fatalf("resolve page: %v", err)
		}
		if !done || len(routes) != 1 {
			b.Fatalf("routes=%d done=%v, want one done route", len(routes), done)
		}
	}
}

func benchmarkDeliveryMessage(channelType uint8, channelID string) channel.Message {
	return channel.Message{
		MessageID:   101,
		MessageSeq:  9,
		ChannelID:   channelID,
		ChannelType: channelType,
		FromUID:     "u1",
		MsgKey:      "bench-key",
		ClientMsgNo: "bench-client-msg-no",
		Timestamp:   int32(time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC).Unix()),
		Payload:     []byte("benchmark realtime delivery payload"),
	}
}

func benchmarkRemoteRoutes(count int, nodeID uint64, uidPrefix string) []deliveryruntime.RouteKey {
	routes := make([]deliveryruntime.RouteKey, 0, count)
	for i := 0; i < count; i++ {
		routes = append(routes, deliveryruntime.RouteKey{
			UID:       uidPrefix + strconv.Itoa(i),
			NodeID:    nodeID,
			BootID:    11,
			SessionID: uint64(i + 1),
		})
	}
	return routes
}

func benchmarkPersonRemoteRoutes(count int, nodeID uint64) []deliveryruntime.RouteKey {
	routes := make([]deliveryruntime.RouteKey, 0, count)
	for i := 0; i < count; i++ {
		uid := "u1"
		if i%2 == 1 {
			uid = "u2"
		}
		routes = append(routes, deliveryruntime.RouteKey{
			UID:       uid,
			NodeID:    nodeID,
			BootID:    11,
			SessionID: uint64(i + 1),
		})
	}
	return routes
}

type benchmarkAcceptAllDeliveryPushClient struct{}

func (benchmarkAcceptAllDeliveryPushClient) PushBatch(_ context.Context, _ uint64, cmd accessnode.DeliveryPushCommand) (accessnode.DeliveryPushResponse, error) {
	return accessnode.DeliveryPushResponse{Accepted: cmd.Routes}, nil
}

func (benchmarkAcceptAllDeliveryPushClient) PushBatchItems(_ context.Context, _ uint64, cmd accessnode.DeliveryPushBatchCommand) (accessnode.DeliveryPushResponse, error) {
	total := 0
	for _, item := range cmd.Items {
		total += len(item.Routes)
	}
	accepted := make([]deliveryruntime.RouteKey, 0, total)
	for _, item := range cmd.Items {
		accepted = append(accepted, item.Routes...)
	}
	return accessnode.DeliveryPushResponse{Accepted: accepted}, nil
}

type benchmarkCommittedDeliverySubmitter struct{}

func (benchmarkCommittedDeliverySubmitter) SubmitCommitted(context.Context, deliveryruntime.CommittedEnvelope) error {
	return nil
}

type benchmarkAuthoritative struct {
	routes map[string][]presence.Route
}

func (a benchmarkAuthoritative) RegisterAuthoritative(context.Context, presence.RegisterAuthoritativeCommand) (presence.RegisterAuthoritativeResult, error) {
	return presence.RegisterAuthoritativeResult{}, nil
}

func (a benchmarkAuthoritative) UnregisterAuthoritative(context.Context, presence.UnregisterAuthoritativeCommand) error {
	return nil
}

func (a benchmarkAuthoritative) HeartbeatAuthoritative(context.Context, presence.HeartbeatAuthoritativeCommand) (presence.HeartbeatAuthoritativeResult, error) {
	return presence.HeartbeatAuthoritativeResult{}, nil
}

func (a benchmarkAuthoritative) ReplayAuthoritative(context.Context, presence.ReplayAuthoritativeCommand) error {
	return nil
}

func (a benchmarkAuthoritative) EndpointsByUID(_ context.Context, uid string) ([]presence.Route, error) {
	return a.routes[uid], nil
}

func (a benchmarkAuthoritative) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]presence.Route, error) {
	out := make(map[string][]presence.Route, len(uids))
	for _, uid := range uids {
		if routes := a.routes[uid]; len(routes) > 0 {
			out[uid] = routes
		}
	}
	return out, nil
}

type benchmarkSession struct {
	id uint64
}

func (s benchmarkSession) ID() uint64 {
	return s.id
}

func (benchmarkSession) Listener() string {
	return "bench"
}

func (benchmarkSession) RemoteAddr() string {
	return ""
}

func (benchmarkSession) LocalAddr() string {
	return ""
}

func (benchmarkSession) WriteFrame(frame.Frame) error {
	return nil
}

func (benchmarkSession) Close() error {
	return nil
}

func (benchmarkSession) SetValue(string, any) {}

func (benchmarkSession) Value(string) any {
	return nil
}
