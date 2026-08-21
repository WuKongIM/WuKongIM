//go:build integration

package replication_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	deliveryinfra "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clientpkg "github.com/WuKongIM/WuKongIM/pkg/client"
	pkggateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// BenchmarkThreeNodeChannelAppendCoupledDelivery1000QPS keeps durable append,
// post-commit plan admission, delivery workers, physical gateway writes, and
// client RECVACK feedback in one causal workload. It is the shortest feedback
// loop that still exercises the product's production-sized channelappend
// pools instead of adding unrelated delivery traffic beside a direct append.
func BenchmarkThreeNodeChannelAppendCoupledDelivery1000QPS(b *testing.B) {
	if b.N < 1000 {
		return
	}
	cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
		useNetwork: true, commitShards: 1, payload: chatLifecycleBenchmarkPayload,
	})
	coupled := newCoupledDeliveryBenchmark(b, cluster, channelAppendDeliveryPressureSessions)
	defer coupled.close(b)
	defer cluster.close(b)

	latencies := make([]time.Duration, b.N)
	jobs := make(chan int, threeNodeBenchmarkWorkers)
	var workers sync.WaitGroup
	workers.Add(threeNodeBenchmarkWorkers)
	for range threeNodeBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				channelIndex := index % len(cluster.channels)
				channel := cluster.channels[channelIndex]
				recipients := coupled.recipients(index)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				started := time.Now()
				future, err := coupled.groups[channel.authority.Leader].SubmitLocal(ctx, coupled.targets[channelIndex], []channelappend.SendBatchItem{{
					Context:  ctx,
					Deadline: started.Add(5 * time.Second),
					Command: channelappend.SendCommand{
						FromUID: "coupled-sender", ClientSeq: uint64(index + 1),
						ClientMsgNo: fmt.Sprintf("coupled-%d", index+1),
						ChannelKey:  string(channel.authority.Key),
						ChannelID:   channel.authority.ChannelID.ID, ChannelType: channel.authority.ChannelID.Type,
						Payload: cluster.payload(index), MessageScopedUIDs: recipients,
					},
				}})
				if err == nil {
					var results []channelappend.SendBatchItemResult
					results, err = future.Wait(ctx)
					if err == nil && (len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != channelappend.ReasonSuccess) {
						if len(results) == 1 && results[0].Err != nil {
							err = results[0].Err
						} else {
							err = fmt.Errorf("unexpected append result: %#v", results)
						}
					}
				}
				latencies[index] = time.Since(started)
				cancel()
				if err != nil {
					cluster.recordError(fmt.Errorf("coupled append channel %d: %w", channelIndex, err))
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	for index := range b.N {
		waitUntil(started.Add(time.Duration(index) * time.Second / threeNodeBenchmarkRate))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()

	if err := cluster.firstError(); err != nil {
		b.Fatal(err)
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	if err := coupled.waitIdle(drainCtx); err != nil {
		b.Fatalf("coupled delivery drain: %v", err)
	}
	b.ReportMetric(float64(coupled.recvAcks.Load())/float64(b.N), "recvacks/op")
	b.Logf("channel stages: %s", cluster.channelObserver.summary())
	cluster.transportObserver.report(b)
	cluster.exchangeObserver.report(b)
	reportDurableQuorumBenchmark(b, "coupled", latencies, cluster.commitObserver.snapshot())
}

type coupledDeliveryBenchmark struct {
	gateway  *pkggateway.Gateway
	handler  *coupledDeliveryBenchmarkHandler
	clients  []*clientpkg.Client
	registry *online.Registry
	delivery *runtimedelivery.Runtime
	groups   map[ch.NodeID]*channelappend.Group
	targets  []channelappend.AuthorityTarget
	uids     []string
	routes   map[string]onlinedelivery.Route
	ids      coupledMessageIDs
	recvAcks atomic.Uint64
}

func newCoupledDeliveryBenchmark(b *testing.B, cluster *durableQuorumBenchmarkCluster, sessionCount int) *coupledDeliveryBenchmark {
	b.Helper()
	h := &coupledDeliveryBenchmark{groups: make(map[ch.NodeID]*channelappend.Group, 3)}
	h.handler = &coupledDeliveryBenchmarkHandler{owner: h}
	gateway, err := pkggateway.New(pkggateway.Options{
		Handler:       h.handler,
		Authenticator: pkggateway.NewWKProtoAuthenticator(pkggateway.WKProtoAuthOptions{DisableEncryption: true}),
		Transport:     pkggateway.TransportOptions{Gnet: pkggateway.GnetTransportOptions{Multicore: true}},
		Listeners:     []pkggateway.ListenerOptions{binding.TCPWKProto("coupled-delivery", "127.0.0.1:0")},
	})
	if err != nil {
		b.Fatalf("gateway.New(): %v", err)
	}
	if err := gateway.Start(); err != nil {
		b.Fatalf("gateway.Start(): %v", err)
	}
	h.gateway = gateway
	h.clients = connectChannelAppendDeliveryPressureClients(b, gateway.ListenerAddr("coupled-delivery"), sessionCount, channelAppendDeliveryClientAutoAck)
	contexts := h.handler.snapshot()
	if len(contexts) != sessionCount {
		h.close(b)
		b.Fatalf("coupled gateway sessions = %d, want %d", len(contexts), sessionCount)
	}

	h.registry = online.NewRegistry(online.RegistryOptions{ShardCount: 64})
	h.routes = make(map[string]onlinedelivery.Route, len(contexts))
	h.uids = make([]string, 0, len(contexts))
	for _, gatewayContext := range contexts {
		uid, _ := gatewayContext.Session.Value(gatewaytypes.SessionValueUID).(string)
		deviceID, _ := gatewayContext.Session.Value(gatewaytypes.SessionValueDeviceID).(string)
		deviceFlag, _ := gatewayContext.Session.Value(gatewaytypes.SessionValueDeviceFlag).(uint8)
		deviceLevel, _ := gatewayContext.Session.Value(gatewaytypes.SessionValueDeviceLevel).(uint8)
		sessionID := gatewayContext.Session.ID()
		ownerRoute := online.OwnerRoute{
			UID: uid, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: sessionID, SessionID: sessionID,
			DeviceID: deviceID, DeviceFlag: deviceFlag, DeviceLevel: deviceLevel,
		}
		if err := h.registry.RegisterPending(online.LocalSession{Route: ownerRoute, Session: coupledGatewaySession{ctx: gatewayContext}}); err != nil {
			h.close(b)
			b.Fatalf("RegisterPending(uid=%q): %v", uid, err)
		}
		if err := h.registry.MarkActive(sessionID); err != nil {
			h.close(b)
			b.Fatalf("MarkActive(uid=%q): %v", uid, err)
		}
		h.uids = append(h.uids, uid)
		h.routes[uid] = onlinedelivery.Route{
			UID: uid, OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: sessionID, SessionID: sessionID,
			DeviceID: deviceID, DeviceFlag: deviceFlag, DeviceLevel: deviceLevel,
		}
	}

	h.delivery = runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{
		LocalNodeID:   1,
		Presence:      coupledDeliveryPresence{routes: h.routes},
		SessionWriter: deliveryinfra.NewLocalSessionWriter(deliveryinfra.LocalSessionWriterOptions{Online: h.registry}),
		QueueSize:     1024, Workers: 100, PlanTimeout: 5 * time.Second,
		MaxPlanRecipients: 512, OwnerPushBatchSize: 512, OwnerConcurrency: 4,
		Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{ShardCount: 64, MaxPendingPerSession: 1024}),
	})
	if err := h.delivery.Start(context.Background()); err != nil {
		h.close(b)
		b.Fatalf("delivery.Start(): %v", err)
	}

	h.targets = make([]channelappend.AuthorityTarget, len(cluster.channels))
	resolver := coupledRecipientAuthorityResolver{}
	for index, channel := range cluster.channels {
		authority := channel.authority
		h.targets[index] = channelappend.AuthorityTarget{
			ChannelID:  channelappend.ChannelID{ID: authority.ChannelID.ID, Type: authority.ChannelID.Type},
			ChannelKey: string(authority.Key), LeaderNodeID: uint64(authority.Leader),
			Epoch: authority.ID.ChannelEpoch, LeaderEpoch: authority.ID.LeaderTerm,
			RouteGeneration: authority.ID.FenceVersion,
		}
	}
	for _, nodeID := range []ch.NodeID{1, 2, 3} {
		group := channelappend.New(channelappend.Options{
			LocalNodeID:         uint64(nodeID),
			Appender:            coupledChannelAppender{node: cluster.nodes[nodeID]},
			MessageID:           &h.ids,
			AuthorityShardCount: max(4, runtime.GOMAXPROCS(0)),
			AdvancePoolSize:     500, EffectPoolSize: 2000,
			RecipientAuthorityResolver: resolver,
			OnlineDeliveryEnqueuer:     h.delivery,
			RecipientBatchSize:         512, SubscriberScanPageSize: 512,
		})
		if err := group.Start(context.Background()); err != nil {
			h.close(b)
			b.Fatalf("channelappend.Start(node=%d): %v", nodeID, err)
		}
		h.groups[nodeID] = group
	}
	return h
}

func (h *coupledDeliveryBenchmark) recipients(index int) []string {
	if index%10 != 0 {
		return []string{h.uids[index%len(h.uids)]}
	}
	groupOrdinal := index / 10
	classOrdinal := groupOrdinal % 100
	count := 5 + groupOrdinal%16
	switch {
	case classOrdinal >= 95:
		count = 1_000 + groupOrdinal%9_001
	case classOrdinal >= 80:
		count = 100 + groupOrdinal%401
	}
	out := make([]string, count)
	out[0] = h.uids[index%len(h.uids)]
	for i := 1; i < count; i++ {
		out[i] = fmt.Sprintf("coupled-offline-%d-%d", groupOrdinal, i)
	}
	return out
}

func (h *coupledDeliveryBenchmark) waitIdle(ctx context.Context) error {
	for _, group := range h.groups {
		if err := group.WaitIdle(ctx); err != nil {
			return err
		}
	}
	for {
		if h.delivery.PendingAckCount() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (h *coupledDeliveryBenchmark) close(b *testing.B) {
	b.Helper()
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for nodeID, group := range h.groups {
		if group != nil {
			if err := group.Stop(ctx); err != nil {
				b.Errorf("channelappend.Stop(node=%d): %v", nodeID, err)
			}
		}
	}
	if h.delivery != nil {
		if err := h.delivery.Stop(ctx); err != nil {
			b.Errorf("delivery.Stop(): %v", err)
		}
	}
	closeChannelAppendDeliveryPressureClients(h.clients)
	if h.gateway != nil {
		if err := h.gateway.Stop(); err != nil {
			b.Errorf("gateway.Stop(): %v", err)
		}
	}
}

type coupledDeliveryBenchmarkHandler struct {
	owner    *coupledDeliveryBenchmark
	mu       sync.Mutex
	sessions []gatewaytypes.Context
}

func (*coupledDeliveryBenchmarkHandler) OnListenerError(string, error) {}

func (h *coupledDeliveryBenchmarkHandler) OnSessionOpen(ctx gatewaytypes.Context) error {
	h.mu.Lock()
	h.sessions = append(h.sessions, ctx)
	h.mu.Unlock()
	return nil
}

func (h *coupledDeliveryBenchmarkHandler) OnFrame(ctx gatewaytypes.Context, packet frame.Frame) error {
	ack, ok := packet.(*frame.RecvackPacket)
	if !ok || h.owner == nil || h.owner.delivery == nil {
		return nil
	}
	uid, _ := ctx.Session.Value(gatewaytypes.SessionValueUID).(string)
	h.owner.recvAcks.Add(1)
	return h.owner.delivery.Recvack(context.Background(), runtimedelivery.Recvack{
		UID: uid, SessionID: ctx.Session.ID(), MessageID: uint64(ack.MessageID), MessageSeq: ack.MessageSeq,
	})
}

func (h *coupledDeliveryBenchmarkHandler) OnSessionClose(ctx gatewaytypes.Context) error {
	if h.owner == nil || h.owner.delivery == nil {
		return nil
	}
	uid, _ := ctx.Session.Value(gatewaytypes.SessionValueUID).(string)
	return h.owner.delivery.SessionClosed(context.Background(), runtimedelivery.SessionClosed{UID: uid, SessionID: ctx.Session.ID()})
}

func (*coupledDeliveryBenchmarkHandler) OnSessionError(gatewaytypes.Context, error) {}

func (h *coupledDeliveryBenchmarkHandler) snapshot() []gatewaytypes.Context {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]gatewaytypes.Context(nil), h.sessions...)
}

type coupledGatewaySession struct{ ctx gatewaytypes.Context }

func (s coupledGatewaySession) WriteDelivery(value any) error {
	packet, ok := value.(frame.Frame)
	if !ok {
		return fmt.Errorf("coupled delivery frame type = %T", value)
	}
	return s.ctx.WriteFrame(packet)
}

func (s coupledGatewaySession) CloseSession(string) error {
	if s.ctx.Session == nil {
		return nil
	}
	return s.ctx.Session.Close()
}

type coupledRecipientAuthorityResolver struct{}

func (coupledRecipientAuthorityResolver) ResolveRecipientAuthority(context.Context, string) (channelappend.RecipientAuthorityTarget, error) {
	return authority.Target{LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1}, nil
}

func (coupledRecipientAuthorityResolver) ResolveRecipientAuthorities(_ context.Context, uids []string) ([]channelappend.RecipientAuthorityResult, error) {
	results := make([]channelappend.RecipientAuthorityResult, len(uids))
	target := authority.Target{LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: 1, AuthorityEpoch: 1}
	for i := range results {
		results[i].Target = target
	}
	return results, nil
}

type coupledDeliveryPresence struct {
	routes map[string]onlinedelivery.Route
}

func (p coupledDeliveryPresence) EndpointsByTargets(_ context.Context, targets []onlinedelivery.RecipientTargetBatch) []runtimedelivery.TargetPresenceResult {
	results := make([]runtimedelivery.TargetPresenceResult, len(targets))
	for i, target := range targets {
		for _, recipient := range target.Recipients {
			if route, ok := p.routes[recipient.UID]; ok {
				results[i].Routes = append(results[i].Routes, route)
			}
		}
	}
	return results
}

var (
	_ gatewaytypes.Handler                          = (*coupledDeliveryBenchmarkHandler)(nil)
	_ online.SessionHandle                          = coupledGatewaySession{}
	_ channelappend.RecipientAuthorityResolver      = coupledRecipientAuthorityResolver{}
	_ channelappend.BatchRecipientAuthorityResolver = coupledRecipientAuthorityResolver{}
	_ runtimedelivery.PlanPresenceResolver          = coupledDeliveryPresence{}
)

type coupledMessageIDs struct{ value atomic.Uint64 }

func (i *coupledMessageIDs) Next() uint64 { return i.value.Add(1) }

type coupledChannelAppender struct{ node ch.Cluster }

func (a coupledChannelAppender) AppendBatch(ctx context.Context, req channelappend.AppendBatchRequest) (channelappend.AppendBatchResult, error) {
	messages := make([]ch.Message, len(req.Messages))
	for i, message := range req.Messages {
		messages[i] = ch.Message{
			MessageID: message.MessageID, MessageSeq: message.MessageSeq,
			ChannelID: message.ChannelID, ChannelType: message.ChannelType, Setting: message.Setting,
			FromUID: message.FromUID, ClientMsgNo: message.ClientMsgNo,
			ServerTimestampMS: message.ServerTimestampMS, TraceID: message.TraceID,
			ChannelKey: message.ChannelKey, SyncOnce: message.SyncOnce,
			Payload: append([]byte(nil), message.Payload...),
		}
	}
	result, err := a.node.AppendBatch(ctx, ch.AppendBatchRequest{
		ChannelID: ch.ChannelID{ID: req.ChannelID.ID, Type: req.ChannelID.Type}, Messages: messages,
		TraceID: req.TraceID, ChannelKey: req.ChannelKey, Attempt: req.Attempt,
		CommitMode: ch.CommitMode(req.CommitMode), ExpectedChannelEpoch: req.ExpectedEpoch,
		ExpectedLeaderEpoch: req.ExpectedLeaderEpoch, OmitResultPayload: req.OmitResultPayload,
		ServerAllocatedMessageIDs: req.ServerAllocatedMessageIDs,
	})
	if err != nil {
		return channelappend.AppendBatchResult{}, err
	}
	items := make([]channelappend.AppendBatchItemResult, len(result.Items))
	for i, item := range result.Items {
		items[i] = channelappend.AppendBatchItemResult{
			MessageID: item.MessageID, MessageSeq: item.MessageSeq, Err: item.Err,
			Message: channelappend.Message{
				MessageID: item.Message.MessageID, MessageSeq: item.Message.MessageSeq,
				ChannelID: item.Message.ChannelID, ChannelType: item.Message.ChannelType,
				Setting: item.Message.Setting, FromUID: item.Message.FromUID,
				ClientMsgNo: item.Message.ClientMsgNo, ServerTimestampMS: item.Message.ServerTimestampMS,
				TraceID: item.Message.TraceID, ChannelKey: item.Message.ChannelKey,
				SyncOnce: item.Message.SyncOnce, Payload: append([]byte(nil), item.Message.Payload...),
			},
		}
	}
	return channelappend.AppendBatchResult{Items: items}, nil
}
