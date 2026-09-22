package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	authoritycontract "github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	deliveryinfra "github.com/WuKongIM/WuKongIM/internal/infra/delivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type recoveryDeliveryOwners struct {
	read    presence.OwnerRouteReader
	started chan struct{}
	release chan struct{}
}

func (o *recoveryDeliveryOwners) RecoverRoutes(ctx context.Context, target presence.RouteTarget, uids []string) ([]presence.Route, error) {
	close(o.started)
	select {
	case <-o.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	snapshot, err := o.read.ReadOwnerRoutes(ctx, target, uids)
	return snapshot.Routes, err
}

type recoveryDeliveryAuthority struct {
	target  presence.RouteTarget
	adapter *clusterinfra.PresenceDirectoryAuthority
}

func (a recoveryDeliveryAuthority) RegisterRoute(ctx context.Context, r presence.Route) (presence.RegisterResult, error) {
	return a.adapter.RegisterRoute(ctx, a.target, r)
}
func (a recoveryDeliveryAuthority) CommitRoute(ctx context.Context, t presence.PendingRouteToken) error {
	return a.adapter.CommitRoute(ctx, a.target, string(t))
}
func (a recoveryDeliveryAuthority) AbortRoute(ctx context.Context, t presence.PendingRouteToken) error {
	return a.adapter.AbortRoute(ctx, a.target, string(t))
}
func (a recoveryDeliveryAuthority) EnqueueUnregister(context.Context, presence.RouteIdentity, uint64) {
}
func (a recoveryDeliveryAuthority) EndpointsByUID(ctx context.Context, uid string) ([]presence.Route, error) {
	return a.adapter.EndpointsByUID(ctx, a.target, uid)
}
func (a recoveryDeliveryAuthority) EndpointsByTargets(ctx context.Context, g []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
	return a.adapter.EndpointsByTargets(ctx, g)
}

type recoveryDeliverySession struct{ frames chan any }

func (s recoveryDeliverySession) WriteDelivery(f any) error { s.frames <- f; return nil }
func (recoveryDeliverySession) CloseSession(string) error   { return nil }

type recoveryOfflineObserver struct{ calls chan struct{} }

func (o recoveryOfflineObserver) ObserveOfflineRecipients(context.Context, runtimedelivery.OfflineRecipientsEvent) {
	o.calls <- struct{}{}
}

func TestPresenceAuthorityRecoveryDeliversCommittedMessageBeforeOwnerTouch(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{})
	session := recoveryDeliverySession{frames: make(chan any, 2)}
	route := online.OwnerRoute{UID: "bob", HashSlot: 101, OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: 8}
	if err := registry.RegisterPending(online.LocalSession{Route: route, Session: session}); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkActive(8); err != nil {
		t.Fatal(err)
	}
	target := presence.RouteTarget{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	directory.BecomeAuthority(target)
	owner := presence.NewOwnerRecovery(registry, 2, 22, func(got presence.RouteTarget) error {
		if got != target {
			return authoritypresence.ErrNotLeader
		}
		return nil
	})
	owners := &recoveryDeliveryOwners{read: owner, started: make(chan struct{}), release: make(chan struct{})}
	adapter := clusterinfra.NewPresenceDirectoryAuthority(directory)
	adapter.SetRecovery(presence.NewAuthorityRecovery(directory, owners))
	lookup := presence.New(presence.Options{Authority: recoveryDeliveryAuthority{target: target, adapter: adapter}})
	offline := recoveryOfflineObserver{calls: make(chan struct{}, 2)}
	runtime := runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{LocalNodeID: 2, Presence: deliveryinfra.NewPresenceResolver(lookup), SessionWriter: deliveryinfra.NewLocalSessionWriter(deliveryinfra.LocalSessionWriterOptions{Online: registry}), OfflineRecipientsObserver: offline, Workers: 1, QueueSize: 1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	event := channelappendcontract.CommittedEnvelope{
		MessageID: 113, MessageSeq: 7, ChannelID: "group", ChannelType: 2, FromUID: "carol",
		Setting: uint8(frame.SettingReceiptEnabled | frame.SettingTopic),
		Topic:   "topic-a", Expire: 3600, Payload: []byte("rejoined"),
	}
	plan := onlinedelivery.RecipientDeliveryPlan{Mode: onlinedelivery.ModeDurable, Event: event, Targets: []onlinedelivery.RecipientTargetBatch{{Target: authoritycontract.Target{HashSlot: 101, SlotID: 5, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}, Recipients: []channelappendcontract.Recipient{{UID: "bob"}}}}}
	if err := runtime.EnqueueRecipientDeliveryPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owners.started:
	case <-ctx.Done():
		t.Fatal("owner reconstruction did not start")
	}
	if _, err := directory.EndpointsByUID(target, "bob"); !errors.Is(err, authoritypresence.ErrRouteNotReady) {
		t.Fatalf("pending reconstruction became offline: %v", err)
	}
	select {
	case <-offline.calls:
		t.Fatal("connected recipient classified offline before owner proof")
	default:
	}
	close(owners.release)
	select {
	case value := <-session.frames:
		recv, ok := value.(*frame.RecvPacket)
		if !ok || recv.MessageID != 113 || recv.MessageSeq != 7 || string(recv.Payload) != "rejoined" {
			t.Fatalf("receive=%+v", value)
		}
		if recv.Setting.Uint8() != event.Setting || recv.Topic != event.Topic || recv.Expire != event.Expire {
			t.Fatalf("delivered setting/topic/expire = %d/%q/%d, want %d/%q/%d", recv.Setting, recv.Topic, recv.Expire, event.Setting, event.Topic, event.Expire)
		}
	case <-ctx.Done():
		t.Fatal("committed message was not delivered after reconstruction")
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-offline.calls:
		t.Fatal("connected recipient classified offline")
	default:
	}
	select {
	case <-session.frames:
		t.Fatal("message delivered twice")
	default:
	}
}

type recoveryBatchDeliveryOwners struct {
	reader presence.OwnerRouteBatchReader
	calls  int
}

func (o *recoveryBatchDeliveryOwners) RecoverRoutes(context.Context, presence.RouteTarget, []string) ([]presence.Route, error) {
	return nil, errors.New("fanout used singleton recovery")
}
func (o *recoveryBatchDeliveryOwners) RecoverRoutesByTargets(ctx context.Context, groups []presence.EndpointLookupGroup) []presence.EndpointLookupResult {
	o.calls++
	snapshots := o.reader.ReadOwnerRoutesByTargets(ctx, groups)
	results := make([]presence.EndpointLookupResult, len(snapshots))
	for i, snapshot := range snapshots {
		results[i] = presence.EndpointLookupResult{Routes: snapshot.Snapshot.Routes, Err: snapshot.Err}
	}
	return results
}

type recoveryFanoutDelivery struct {
	uid   string
	frame any
}
type recoveryFanoutSession struct {
	uid    string
	frames chan recoveryFanoutDelivery
}

func (s recoveryFanoutSession) WriteDelivery(f any) error {
	s.frames <- recoveryFanoutDelivery{uid: s.uid, frame: f}
	return nil
}
func (recoveryFanoutSession) CloseSession(string) error { return nil }

// One default 512-recipient committed plan spans every default Hash Slot and
// must resolve through one owner page before exact-once local writes.
func TestPresenceRecoveryBatchedFanoutDeliversEveryCommittedRecipient(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{})
	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	frames := make(chan recoveryFanoutDelivery, 512)
	plan := onlinedelivery.RecipientDeliveryPlan{Mode: onlinedelivery.ModeDurable, Event: channelappendcontract.CommittedEnvelope{MessageID: 113, MessageSeq: 7, ChannelID: "group", ChannelType: 2, FromUID: "sender", Payload: []byte("fanout")}}
	for i := range 256 {
		target := presence.RouteTarget{HashSlot: uint16(i), SlotID: uint32(i % 12), LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}
		directory.BecomeAuthority(target)
		group := onlinedelivery.RecipientTargetBatch{Target: authoritycontract.Target{HashSlot: target.HashSlot, SlotID: target.SlotID, LeaderNodeID: 1, LeaderTerm: 4, ConfigEpoch: 1}}
		for j := range 2 {
			uid := fmt.Sprintf("fanout-%d-%d", i, j)
			id := uint64(i*2 + j + 1)
			group.Recipients = append(group.Recipients, channelappendcontract.Recipient{UID: uid})
			route := online.OwnerRoute{UID: uid, HashSlot: uint16(i), OwnerNodeID: 2, OwnerBootID: 22, OwnerSeq: 1, SessionID: id}
			if err := registry.RegisterPending(online.LocalSession{Route: route, Session: recoveryFanoutSession{uid: uid, frames: frames}}); err != nil {
				t.Fatal(err)
			}
			if err := registry.MarkActive(id); err != nil {
				t.Fatal(err)
			}
		}
		plan.Targets = append(plan.Targets, group)
	}
	owner := presence.NewOwnerRecovery(registry, 2, 22, func(target presence.RouteTarget) error { _, err := directory.RecoveryUIDs(target, nil); return err })
	owners := &recoveryBatchDeliveryOwners{reader: owner}
	adapter := clusterinfra.NewPresenceDirectoryAuthority(directory)
	adapter.SetRecovery(presence.NewAuthorityRecovery(directory, owners))
	lookup := presence.New(presence.Options{Authority: recoveryDeliveryAuthority{adapter: adapter}})
	offline := recoveryOfflineObserver{calls: make(chan struct{}, 1)}
	runtime := runtimedelivery.NewRuntime(runtimedelivery.RuntimeOptions{LocalNodeID: 2, Presence: deliveryinfra.NewPresenceResolver(lookup), SessionWriter: deliveryinfra.NewLocalSessionWriter(deliveryinfra.LocalSessionWriterOptions{Online: registry}), OfflineRecipientsObserver: offline, Workers: 1, QueueSize: 1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })
	if err := runtime.EnqueueRecipientDeliveryPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if owners.calls != 1 || len(frames) != 512 {
		t.Fatalf("owner rounds=%d deliveries=%d, want 1/512", owners.calls, len(frames))
	}
	seen := make(map[string]int, 512)
	for range 512 {
		value := <-frames
		seen[value.uid]++
		recv, ok := value.frame.(*frame.RecvPacket)
		if !ok || recv.MessageID != 113 || recv.MessageSeq != 7 || string(recv.Payload) != "fanout" {
			t.Fatalf("receive=%+v", value)
		}
	}
	for _, group := range plan.Targets {
		for _, recipient := range group.Recipients {
			if seen[recipient.UID] != 1 {
				t.Fatalf("recipient %s delivered %d times", recipient.UID, seen[recipient.UID])
			}
		}
	}
	select {
	case <-offline.calls:
		t.Fatal("connected recipient classified offline")
	default:
	}
}
