package app

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	infracluster "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	infradelivery "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	productionRecipientBenchmarkRecipients = 512
	productionRecipientBenchmarkTargets    = 221
	productionRecipientBenchmarkOnline     = 55
)

// productionRecipientBenchmarkSession is the concrete owner-session boundary
// used by the benchmark. It validates real RecvPacket construction instead of
// replacing the owner push with a no-op pusher.
type productionRecipientBenchmarkSession struct {
	writes uint64
}

// WriteDelivery validates and records one owner-local production delivery.
func (s *productionRecipientBenchmarkSession) WriteDelivery(value any) error {
	if _, ok := value.(*frame.RecvPacket); !ok {
		return fmt.Errorf("delivery payload type = %T, want *frame.RecvPacket", value)
	}
	s.writes++
	return nil
}

// CloseSession satisfies the owner-local session boundary.
func (*productionRecipientBenchmarkSession) CloseSession(string) error {
	return nil
}

// productionRecipientBenchmarkFixture retains the real runtime components and
// immutable Cloud Medium-shaped work shared by baseline and candidate runs.
type productionRecipientBenchmarkFixture struct {
	delivery *runtimedelivery.Runtime
	observer *productionRecipientBenchmarkObserver
	plan     onlinedelivery.RecipientDeliveryPlan
	acks     []runtimedelivery.Recvack
	sessions []*productionRecipientBenchmarkSession
}

type productionRecipientBenchmarkObserver struct {
	terminal chan runtimedelivery.PlanTerminalEvent
}

func (*productionRecipientBenchmarkObserver) ObservePlanAdmission(runtimedelivery.PlanAdmissionEvent) {
}

func (o *productionRecipientBenchmarkObserver) ObservePlanTerminal(event runtimedelivery.PlanTerminalEvent) {
	o.terminal <- event
}

func (*productionRecipientBenchmarkObserver) SetRuntimePressure(runtimedelivery.RuntimePressureEvent) {
}

func (*productionRecipientBenchmarkObserver) ObserveOwnerPush(runtimedelivery.OwnerPushEvent) {
}

func (o *productionRecipientBenchmarkObserver) waitTerminal(ctx context.Context) (runtimedelivery.PlanTerminalEvent, error) {
	select {
	case event := <-o.terminal:
		return event, nil
	case <-ctx.Done():
		return runtimedelivery.PlanTerminalEvent{}, ctx.Err()
	}
}

// productionRecipientBenchmarkPresenceNode is the stable cluster-routing seam
// required by the production PresenceAuthorityClient. Happy-path exact-target
// endpoint lookups use only NodeID; the complete route table keeps stale-target
// retry behavior valid without starting a cluster or node RPC transport.
type productionRecipientBenchmarkPresenceNode struct {
	routes          map[string]pkgcluster.Route
	byHashSlot      map[uint16]pkgcluster.Route
	authorityEvents chan pkgcluster.RouteAuthorityEvent
}

// NodeID identifies the local production authority and owner node.
func (*productionRecipientBenchmarkPresenceNode) NodeID() uint64 {
	return 1
}

// RouteKey returns the fixed route snapshot for one benchmark UID.
func (n *productionRecipientBenchmarkPresenceNode) RouteKey(uid string) (pkgcluster.Route, error) {
	route, ok := n.routes[uid]
	if !ok {
		return pkgcluster.Route{}, fmt.Errorf("benchmark route for uid %q not found", uid)
	}
	return route, nil
}

// RouteKeysPartial returns item-aligned routes from the same fixed snapshot.
func (n *productionRecipientBenchmarkPresenceNode) RouteKeysPartial(uids []string) ([]pkgcluster.RouteKeyResult, error) {
	results := make([]pkgcluster.RouteKeyResult, len(uids))
	for i, uid := range uids {
		route, err := n.RouteKey(uid)
		results[i] = pkgcluster.RouteKeyResult{Route: route, Err: err}
	}
	return results, nil
}

// RouteHashSlot returns the fixed route for one physical hash slot.
func (n *productionRecipientBenchmarkPresenceNode) RouteHashSlot(hashSlot uint16) (pkgcluster.Route, error) {
	route, ok := n.byHashSlot[hashSlot]
	if !ok {
		return pkgcluster.Route{}, fmt.Errorf("benchmark route for hash slot %d not found", hashSlot)
	}
	return route, nil
}

// CallRPC rejects unexpected remote traffic because every benchmark target is
// deliberately local; remote transport is outside this benchmark's scope.
func (*productionRecipientBenchmarkPresenceNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	return nil, fmt.Errorf("benchmark unexpected remote presence RPC")
}

// RegisterRPC is unused because the fixture directly supplies the local
// production authority adapter.
func (*productionRecipientBenchmarkPresenceNode) RegisterRPC(uint8, pkgcluster.NodeRPCHandler) {
}

// WatchRouteAuthorities exposes the stable, silent route-watch surface
// required by the production presence client.
func (n *productionRecipientBenchmarkPresenceNode) WatchRouteAuthorities() <-chan pkgcluster.RouteAuthorityEvent {
	return n.authorityEvents
}

// BenchmarkRecipientDeliveryProductionPathCloudMedium exercises the shared
// production recipient path from exact recipient-authority groups through the
// presence directory, owner-local packet writes, pending-ACK binding, and
// RECVACK removal. It excludes channel persistence, network transport, and
// client protocol encoding; those require the three-node benchmark gate.
func BenchmarkRecipientDeliveryProductionPathCloudMedium(b *testing.B) {
	fixture := newProductionRecipientBenchmarkFixture(b)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	b.ReportAllocs()
	b.ReportMetric(productionRecipientBenchmarkRecipients, "recipients/op")
	b.ReportMetric(productionRecipientBenchmarkTargets, "target-groups/op")
	b.ReportMetric(productionRecipientBenchmarkOnline, "online-routes/op")
	b.ReportMetric(productionRecipientBenchmarkOnline, "recvacks/op")
	b.SetBytes(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		messageID := uint64(i + 1)
		fixture.plan.Event.MessageID = messageID
		fixture.plan.Event.MessageSeq = messageID
		if err := fixture.delivery.EnqueueRecipientDeliveryPlan(ctx, fixture.plan); err != nil {
			b.Fatalf("EnqueueRecipientDeliveryPlan() error = %v", err)
		}
		terminal, err := fixture.observer.waitTerminal(ctx)
		if err != nil {
			b.Fatalf("wait for Online Delivery terminal event: %v", err)
		}
		if terminal.Result != runtimedelivery.ObservationResultOK || terminal.Recipients != productionRecipientBenchmarkRecipients {
			b.Fatalf("Online Delivery terminal event = %#v, want ok with %d recipients", terminal, productionRecipientBenchmarkRecipients)
		}
		for ackIndex := range fixture.acks {
			ack := fixture.acks[ackIndex]
			ack.MessageID = messageID
			ack.MessageSeq = messageID
			if err := fixture.delivery.Recvack(ctx, ack); err != nil {
				b.Fatalf("Recvack() index %d error = %v", ackIndex, err)
			}
		}
	}
	b.StopTimer()

	if got := fixture.delivery.PendingAckCount(); got != 0 {
		b.Fatalf("PendingAckCount() = %d, want 0 after matched RECVACKs", got)
	}
	writes := uint64(0)
	for _, session := range fixture.sessions {
		writes += session.writes
	}
	if want := uint64(b.N * productionRecipientBenchmarkOnline); writes != want {
		b.Fatalf("owner-local writes = %d, want %d", writes, want)
	}
}

func newProductionRecipientBenchmarkFixture(tb testing.TB) productionRecipientBenchmarkFixture {
	tb.Helper()
	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{
		LocalNodeID: 1,
		ShardCount:  32,
	})
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 32})
	presenceNode := &productionRecipientBenchmarkPresenceNode{
		routes:          make(map[string]pkgcluster.Route, productionRecipientBenchmarkRecipients),
		byHashSlot:      make(map[uint16]pkgcluster.Route, productionRecipientBenchmarkTargets),
		authorityEvents: make(chan pkgcluster.RouteAuthorityEvent),
	}
	plan := onlinedelivery.RecipientDeliveryPlan{
		Mode: onlinedelivery.ModeDurable,
		Event: channelappend.CommittedEnvelope{
			ChannelID:   "benchmark-cloud-medium",
			ChannelType: 2,
			FromUID:     "benchmark-sender",
			Payload:     make([]byte, 256),
		},
		Targets: make([]onlinedelivery.RecipientTargetBatch, productionRecipientBenchmarkTargets),
	}
	authorityTargets := make([]authoritypresence.RouteTarget, productionRecipientBenchmarkTargets)
	for targetIndex := range plan.Targets {
		target := authoritypresence.RouteTarget{
			HashSlot:       uint16(targetIndex),
			SlotID:         uint32(targetIndex%10 + 1),
			LeaderNodeID:   1,
			LeaderTerm:     1,
			ConfigEpoch:    1,
			RouteRevision:  1,
			AuthorityEpoch: 1,
		}
		authorityTargets[targetIndex] = target
		presenceNode.byHashSlot[target.HashSlot] = pkgcluster.Route{
			HashSlot:       target.HashSlot,
			SlotID:         target.SlotID,
			Leader:         target.LeaderNodeID,
			LeaderTerm:     target.LeaderTerm,
			ConfigEpoch:    target.ConfigEpoch,
			Revision:       target.RouteRevision,
			AuthorityEpoch: target.AuthorityEpoch,
		}
		directory.BecomeAuthority(target)
		plan.Targets[targetIndex].Target = channelappend.RecipientAuthorityTarget{
			HashSlot:       target.HashSlot,
			SlotID:         target.SlotID,
			LeaderNodeID:   target.LeaderNodeID,
			LeaderTerm:     target.LeaderTerm,
			ConfigEpoch:    target.ConfigEpoch,
			RouteRevision:  target.RouteRevision,
			AuthorityEpoch: target.AuthorityEpoch,
		}
	}

	acks := make([]runtimedelivery.Recvack, 0, productionRecipientBenchmarkOnline)
	sessions := make([]*productionRecipientBenchmarkSession, 0, productionRecipientBenchmarkOnline)
	for recipientIndex := 0; recipientIndex < productionRecipientBenchmarkRecipients; recipientIndex++ {
		uid := "benchmark-recipient-" + strconv.Itoa(recipientIndex)
		targetIndex := recipientIndex % productionRecipientBenchmarkTargets
		presenceNode.routes[uid] = presenceNode.byHashSlot[authorityTargets[targetIndex].HashSlot]
		plan.Targets[targetIndex].Recipients = append(
			plan.Targets[targetIndex].Recipients,
			channelappend.Recipient{UID: uid},
		)
		if recipientIndex >= productionRecipientBenchmarkOnline {
			continue
		}
		sessionID := uint64(recipientIndex + 1)
		route := authoritypresence.Route{
			UID:           uid,
			OwnerNodeID:   1,
			OwnerBootID:   1,
			OwnerSeq:      sessionID,
			SessionID:     sessionID,
			ConnectedUnix: 1,
			LastSeenUnix:  1,
		}
		registered, err := directory.RegisterRoute(authorityTargets[targetIndex], route)
		if err != nil {
			tb.Fatalf("RegisterRoute() recipient %d error = %v", recipientIndex, err)
		}
		if registered.PendingToken != "" || len(registered.Actions) != 0 {
			tb.Fatalf("RegisterRoute() recipient %d result = %#v, want immediate active route", recipientIndex, registered)
		}
		session := &productionRecipientBenchmarkSession{}
		if err := registry.RegisterPending(online.LocalSession{
			Route: online.OwnerRoute{
				UID:              route.UID,
				HashSlot:         authorityTargets[targetIndex].HashSlot,
				OwnerNodeID:      route.OwnerNodeID,
				OwnerBootID:      route.OwnerBootID,
				OwnerSeq:         route.OwnerSeq,
				SessionID:        route.SessionID,
				ConnectedUnix:    route.ConnectedUnix,
				LastActivityUnix: route.LastSeenUnix,
			},
			Session: session,
		}); err != nil {
			tb.Fatalf("RegisterPending() recipient %d error = %v", recipientIndex, err)
		}
		if err := registry.MarkActive(sessionID); err != nil {
			tb.Fatalf("MarkActive() recipient %d error = %v", recipientIndex, err)
		}
		acks = append(acks, runtimedelivery.Recvack{UID: uid, SessionID: sessionID})
		sessions = append(sessions, session)
	}

	presenceClient := infracluster.NewPresenceAuthorityClient(
		presenceNode,
		infracluster.NewPresenceDirectoryAuthority(directory),
	)
	presenceApp := presenceusecase.New(presenceusecase.Options{Authority: presenceClient})
	observer := &productionRecipientBenchmarkObserver{terminal: make(chan runtimedelivery.PlanTerminalEvent, 1)}
	deliveryRuntime := runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{
		LocalNodeID:        1,
		Presence:           infradelivery.NewPresenceResolver(presenceApp),
		SessionWriter:      infradelivery.NewLocalSessionWriter(infradelivery.LocalSessionWriterOptions{Online: registry}),
		QueueSize:          1,
		Workers:            1,
		MaxPlanRecipients:  productionRecipientBenchmarkRecipients,
		OwnerPushBatchSize: productionRecipientBenchmarkRecipients,
		RetryMaxAttempts:   1,
		Observer:           observer,
		Acks: runtimedelivery.NewAckTracker(runtimedelivery.AckTrackerOptions{
			ShardCount:           32,
			MaxPendingPerSession: 1024,
		}),
	})
	if err := deliveryRuntime.Start(context.Background()); err != nil {
		tb.Fatalf("start Online Delivery runtime: %v", err)
	}
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deliveryRuntime.Stop(ctx); err != nil {
			tb.Errorf("stop Online Delivery runtime: %v", err)
		}
	})
	return productionRecipientBenchmarkFixture{
		delivery: deliveryRuntime,
		observer: observer,
		plan:     plan,
		acks:     acks,
		sessions: sessions,
	}
}
