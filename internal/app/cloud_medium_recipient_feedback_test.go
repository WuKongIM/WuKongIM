package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clusterrouting "github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	cloudMediumFeedbackHashSlots                   = 256
	cloudMediumFeedbackLogicalSlots                = 10
	cloudMediumFeedbackRecipientBatchSize          = 512
	cloudMediumFeedbackRecipientWorkers            = 320
	cloudMediumFeedbackMessageCount                = 250
	cloudMediumFeedbackPlanCount                   = 255
	cloudMediumFeedbackRecipientRows               = 19_650
	cloudMediumFeedbackOnlineRoutes                = 2_670
	cloudMediumFeedbackOwnerWrites                 = 2_545
	cloudMediumFeedbackGroupSubscriberPlans        = 130
	cloudMediumFeedbackGroupSubscriberRows         = 19_400
	cloudMediumFeedbackEquivalentIngressQPS        = 5_000
	cloudMediumFeedbackSliceDuration               = 50 * time.Millisecond
	cloudMediumFeedbackRouteRevision        uint64 = 11
	cloudMediumFeedbackAuthorityEpoch       uint64 = 13
)

// cloudMediumFeedbackProfiles is the smallest integral expansion of the
// Cloud Medium traffic mix in docker/sim/cloud-medium.yaml. One row describes
// how many messages, subscriber rows, and online recipients occur in one
// twentieth of a second.
//
// Important: 250 messages at 5,000 messages/s is a 50ms slice, not a 20ms
// slice. Calling this a 20ms slice would overstate ingress by 2.5x.
var cloudMediumFeedbackProfiles = []struct {
	messages   int
	recipients int
	online     int
	person     bool
}{
	{messages: 100, recipients: 2, online: 2, person: true},
	{messages: 25, recipients: 2, online: 2, person: true},
	{messages: 60, recipients: 20, online: 5},
	{messages: 42, recipients: 100, online: 15},
	{messages: 18, recipients: 500, online: 55},
	{messages: 5, recipients: 1_000, online: 100},
}

// TestCloudMediumRecipientOneTwentiethSecondFeedback exercises the real local
// composition seam from channelappend post-commit recipient dispatch through:
//
//   - the real 256-physical-hash-slot / 10-logical-Slot routing table;
//   - channelAppendRecipientResolver and PresenceAuthorityClient exact-target
//     adapters over the real in-memory presence directory;
//   - the bounded recipient delivery queue with 320 workers;
//   - localOwnerPusher and the real pending-ACK state machine, with sessions
//     returning an immediate RECVACK from inside WriteDelivery.
//
// The durable appender, Slot/Channel Raft, Pebble, cluster transport, gateway
// socket, and client protocol loop are deterministic in-process substitutes.
// Channels and recipient UIDs are unique inside the slice so every counted row
// is independently conserved; hot-channel cache reuse and overlapping users
// remain separate focused-test concerns.
// The slice is not wall-clock paced. Therefore this test proves shape,
// conservation, bounded drain, and ACK lifecycle correctness; it does not
// prove production QPS or latency percentiles.
func TestCloudMediumRecipientOneTwentiethSecondFeedback(t *testing.T) {
	if got := cloudMediumFeedbackMessageCount * int(time.Second/cloudMediumFeedbackSliceDuration); got != cloudMediumFeedbackEquivalentIngressQPS {
		t.Fatalf("slice ingress = %d messages/s, want %d", got, cloudMediumFeedbackEquivalentIngressQPS)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	harness, err := newCloudMediumRecipientFeedbackHarness()
	if err != nil {
		t.Fatalf("newCloudMediumRecipientFeedbackHarness() error = %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := harness.close(cleanupCtx); err != nil {
			t.Errorf("harness.close() error = %v", err)
		}
	}()

	if _, err := harness.run(ctx); err != nil {
		t.Fatalf("harness.run() error = %v", err)
	}
}

// BenchmarkCloudMediumRecipientOneTwentiethSecondFeedback reports allocations
// for one deterministic integrated volume slice. Run it explicitly with a
// fixed iteration count, for example -bench OneTwentiethSecond -benchtime=1x.
// Its ns/op is a local feedback signal only: the benchmark intentionally has
// no real network, Raft, Pebble, socket backpressure, or 50ms pacing and must
// never be presented as Cloud Medium QPS or P99 evidence.
func BenchmarkCloudMediumRecipientOneTwentiethSecondFeedback(b *testing.B) {
	b.ReportAllocs()
	b.StopTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		harness, err := newCloudMediumRecipientFeedbackHarness()
		if err != nil {
			cancel()
			b.Fatalf("newCloudMediumRecipientFeedbackHarness() error = %v", err)
		}

		b.StartTimer()
		snapshot, runErr := harness.run(ctx)
		b.StopTimer()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		closeErr := harness.close(cleanupCtx)
		cleanupCancel()
		cancel()
		if runErr != nil {
			b.Fatalf("harness.run() error = %v", runErr)
		}
		if closeErr != nil {
			b.Fatalf("harness.close() error = %v", closeErr)
		}
		b.ReportMetric(float64(snapshot.maxQueueDepth), "max-queue-depth/op")
		b.ReportMetric(float64(snapshot.maxInflight), "max-inflight/op")
		b.ReportMetric(cloudMediumFeedbackMessageCount, "messages/op")
		b.ReportMetric(cloudMediumFeedbackPlanCount, "plans/op")
		b.ReportMetric(cloudMediumFeedbackRecipientRows, "recipient-rows/op")
	}
}

type cloudMediumRecipientFeedbackHarness struct {
	group        *channelappend.Group
	worker       *channelappend.RecipientDeliveryWorker
	observer     *cloudMediumRecipientFeedbackObserver
	node         *cloudMediumRecipientFeedbackNode
	source       *cloudMediumRecipientFeedbackSubscribers
	directory    *authoritypresence.Directory
	delivery     *runtimedelivery.Manager
	writes       *atomic.Int64
	targets      []channelappend.AuthorityTarget
	items        []channelappend.SendBatchItem
	closeMu      sync.Mutex
	groupClosed  bool
	workerClosed bool
}

func newCloudMediumRecipientFeedbackHarness() (*cloudMediumRecipientFeedbackHarness, error) {
	node, err := newCloudMediumRecipientFeedbackNode()
	if err != nil {
		return nil, err
	}
	directory := authoritypresence.NewDirectory(authoritypresence.DirectoryOptions{
		LocalNodeID: 1,
		ShardCount:  cloudMediumFeedbackLogicalSlots,
	})
	targetByHashSlot := make([]authoritypresence.RouteTarget, cloudMediumFeedbackHashSlots)
	for hashSlot := 0; hashSlot < cloudMediumFeedbackHashSlots; hashSlot++ {
		route, routeErr := node.RouteHashSlot(uint16(hashSlot))
		if routeErr != nil {
			return nil, fmt.Errorf("route hash slot %d: %w", hashSlot, routeErr)
		}
		target := cloudMediumPresenceTarget(route)
		targetByHashSlot[hashSlot] = target
		directory.BecomeAuthority(target)
	}

	onlineRegistry := online.NewRegistry(online.RegistryOptions{ShardCount: 32})
	deliveryManager := runtimedelivery.NewManager(runtimedelivery.ManagerOptions{})
	writes := &atomic.Int64{}
	source := &cloudMediumRecipientFeedbackSubscribers{byChannel: make(map[string][]channelappend.Recipient, cloudMediumFeedbackMessageCount)}
	targets := make([]channelappend.AuthorityTarget, 0, cloudMediumFeedbackMessageCount)
	items := make([]channelappend.SendBatchItem, 0, cloudMediumFeedbackMessageCount)
	nextSessionID := uint64(1)
	messageIndex := 0
	messageCount := 0
	planCount := 0
	recipientRows := 0
	onlineRoutes := 0
	ownerWrites := 0
	for profileIndex, profile := range cloudMediumFeedbackProfiles {
		if profile.online > profile.recipients {
			return nil, fmt.Errorf("profile %d online recipients %d exceed recipients %d", profileIndex, profile.online, profile.recipients)
		}
		for profileMessage := 0; profileMessage < profile.messages; profileMessage++ {
			channelID := fmt.Sprintf("cloud-medium-feedback-%d-%d", profileIndex, profileMessage)
			recipients := make([]channelappend.Recipient, profile.recipients)
			firstSessionID := uint64(0)
			for recipientIndex := 0; recipientIndex < profile.recipients; recipientIndex++ {
				uid := fmt.Sprintf("feedback-%d-%d-%d", profileIndex, profileMessage, recipientIndex)
				recipients[recipientIndex] = channelappend.Recipient{UID: uid}
				if recipientIndex >= profile.online {
					continue
				}
				hashSlot := clusterrouting.HashSlotForKey(uid, cloudMediumFeedbackHashSlots)
				target := targetByHashSlot[hashSlot]
				route := authoritypresence.Route{
					UID:           uid,
					OwnerNodeID:   1,
					OwnerBootID:   1,
					OwnerSeq:      nextSessionID,
					SessionID:     nextSessionID,
					DeviceID:      "feedback-device",
					DeviceFlag:    1,
					DeviceLevel:   1,
					ConnectedUnix: 1,
					LastSeenUnix:  1,
				}
				registered, registerErr := directory.RegisterRoute(target, route)
				if registerErr != nil {
					return nil, fmt.Errorf("register presence route uid=%q: %w", uid, registerErr)
				}
				if registered.PendingToken != "" || len(registered.Actions) != 0 {
					return nil, fmt.Errorf("unique feedback route uid=%q unexpectedly requires conflict commit", uid)
				}
				session := &cloudMediumRecipientFeedbackSession{
					uid:       uid,
					sessionID: nextSessionID,
					delivery:  deliveryManager,
					writes:    writes,
				}
				ownerRoute := online.OwnerRoute{
					UID:              uid,
					HashSlot:         hashSlot,
					OwnerNodeID:      route.OwnerNodeID,
					OwnerBootID:      route.OwnerBootID,
					OwnerSeq:         route.OwnerSeq,
					SessionID:        route.SessionID,
					DeviceID:         route.DeviceID,
					DeviceFlag:       route.DeviceFlag,
					DeviceLevel:      route.DeviceLevel,
					ConnectedUnix:    route.ConnectedUnix,
					LastActivityUnix: route.LastSeenUnix,
				}
				if registerErr := onlineRegistry.RegisterPending(online.LocalSession{Route: ownerRoute, Session: session}); registerErr != nil {
					return nil, fmt.Errorf("register local session uid=%q: %w", uid, registerErr)
				}
				if activeErr := onlineRegistry.MarkActive(nextSessionID); activeErr != nil {
					return nil, fmt.Errorf("activate local session uid=%q: %w", uid, activeErr)
				}
				if recipientIndex == 0 {
					firstSessionID = nextSessionID
				}
				nextSessionID++
				onlineRoutes++
			}
			channelType := frame.ChannelTypeGroup
			senderUID := fmt.Sprintf("feedback-sender-%d", messageIndex)
			senderNodeID := uint64(0)
			senderSessionID := uint64(0)
			if profile.person {
				channelType = frame.ChannelTypePerson
				senderUID = recipients[0].UID
				senderNodeID = 1
				senderSessionID = firstSessionID
				channelID = runtimechannelid.EncodePersonChannel(recipients[0].UID, recipients[1].UID)
				// The sender's exact gateway session is suppressed by the real
				// recipient processor, so only the peer receives an owner write.
				ownerWrites += profile.online - 1
			} else {
				source.byChannel[channelID] = recipients
				ownerWrites += profile.online
			}
			targets = append(targets, channelappend.AuthorityTarget{
				ChannelID:    channelappend.ChannelID{ID: channelID, Type: channelType},
				ChannelKey:   strconv.Itoa(int(channelType)) + ":" + channelID,
				LeaderNodeID: 1,
				Epoch:        1,
				LeaderEpoch:  1,
				Large:        true,
			})
			items = append(items, channelappend.SendBatchItem{
				Context: context.Background(),
				Command: channelappend.SendCommand{
					FromUID:         senderUID,
					SenderNodeID:    senderNodeID,
					SenderSessionID: senderSessionID,
					ChannelID:       channelID,
					ChannelType:     channelType,
					ClientMsgNo:     fmt.Sprintf("feedback-message-%d", messageIndex),
					Payload:         []byte("cloud-medium-feedback"),
				},
			})
			messageIndex++
			messageCount++
			recipientRows += profile.recipients
			planCount += (profile.recipients + cloudMediumFeedbackRecipientBatchSize - 1) / cloudMediumFeedbackRecipientBatchSize
		}
	}
	if messageCount != cloudMediumFeedbackMessageCount || planCount != cloudMediumFeedbackPlanCount || recipientRows != cloudMediumFeedbackRecipientRows || onlineRoutes != cloudMediumFeedbackOnlineRoutes || ownerWrites != cloudMediumFeedbackOwnerWrites {
		return nil, fmt.Errorf("feedback fixture shape messages=%d plans=%d rows=%d onlineRoutes=%d ownerWrites=%d, want %d/%d/%d/%d/%d", messageCount, planCount, recipientRows, onlineRoutes, ownerWrites, cloudMediumFeedbackMessageCount, cloudMediumFeedbackPlanCount, cloudMediumFeedbackRecipientRows, cloudMediumFeedbackOnlineRoutes, cloudMediumFeedbackOwnerWrites)
	}

	presenceClient := clusterinfra.NewPresenceAuthorityClient(node, presenceDirectoryAuthority{directory: directory})
	presenceApp := presenceusecase.New(presenceusecase.Options{Authority: presenceClient})
	localPusher := &localOwnerPusher{online: onlineRegistry, delivery: deliveryManager}
	processor := channelappend.NewRecipientProcessor(channelappend.RecipientProcessorOptions{
		PresenceResolver:   channelAppendPresenceResolver{presence: presenceApp},
		OwnerPusher:        channelAppendOwnerPusher{next: localPusher},
		OwnerPushBatchSize: cloudMediumFeedbackRecipientBatchSize,
	})
	observer := newCloudMediumRecipientFeedbackObserver()
	worker := channelappend.NewRecipientDeliveryWorker(channelappend.RecipientDeliveryWorkerOptions{
		Processor:   processor,
		QueueSize:   512,
		Workers:     cloudMediumFeedbackRecipientWorkers,
		PlanTimeout: 10 * time.Second,
		Observer:    observer,
	})
	if err := worker.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("start recipient delivery worker: %w", err)
	}
	group := channelappend.New(channelappend.Options{
		LocalNodeID:                           1,
		Appender:                              &cloudMediumRecipientFeedbackAppender{},
		MessageID:                             &cloudMediumRecipientFeedbackMessageIDs{},
		AuthorityShardCount:                   cloudMediumFeedbackLogicalSlots,
		AdvancePoolSize:                       8,
		AdmissionCapacityPerShard:             512,
		ChannelBacklogHighWatermark:           512,
		PostCommitHandoffCapacity:             512,
		EffectPoolSize:                        16,
		InboxCoalesceWindow:                   -time.Nanosecond,
		Observer:                              observer,
		Subscribers:                           source,
		RecipientAuthorityResolver:            channelAppendRecipientResolver{node: node},
		RecipientDeliveryEnqueuer:             worker,
		SubscriberScanPageSize:                cloudMediumFeedbackRecipientBatchSize,
		RecipientBatchSize:                    cloudMediumFeedbackRecipientBatchSize,
		RecipientAuthorityDispatchConcurrency: 16,
	})
	if err := group.Start(context.Background()); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = worker.Stop(stopCtx)
		return nil, fmt.Errorf("start channel append group: %w", err)
	}
	return &cloudMediumRecipientFeedbackHarness{
		group:     group,
		worker:    worker,
		observer:  observer,
		node:      node,
		source:    source,
		directory: directory,
		delivery:  deliveryManager,
		writes:    writes,
		targets:   targets,
		items:     items,
	}, nil
}

func (h *cloudMediumRecipientFeedbackHarness) run(ctx context.Context) (cloudMediumRecipientFeedbackSnapshot, error) {
	if h == nil || h.group == nil {
		return cloudMediumRecipientFeedbackSnapshot{}, errors.New("cloud medium feedback harness is not initialized")
	}
	for i := range h.items {
		future, err := h.group.SubmitLocal(ctx, h.targets[i], []channelappend.SendBatchItem{h.items[i]})
		if err != nil {
			return cloudMediumRecipientFeedbackSnapshot{}, fmt.Errorf("submit message %d: %w", i, err)
		}
		results, err := future.Wait(ctx)
		if err != nil {
			return cloudMediumRecipientFeedbackSnapshot{}, fmt.Errorf("wait message %d: %w", i, err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != channelappend.ReasonSuccess {
			return cloudMediumRecipientFeedbackSnapshot{}, fmt.Errorf("message %d result = %#v, want one success", i, results)
		}
	}
	if err := h.stopGroup(ctx); err != nil {
		return cloudMediumRecipientFeedbackSnapshot{}, fmt.Errorf("drain channel append group: %w", err)
	}
	if err := h.observer.waitDrained(ctx, cloudMediumFeedbackPlanCount); err != nil {
		return cloudMediumRecipientFeedbackSnapshot{}, err
	}
	snapshot := h.snapshot()
	if err := snapshot.validate(); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (h *cloudMediumRecipientFeedbackHarness) close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	groupErr := h.stopGroup(ctx)
	workerErr := h.stopWorker(ctx)
	return errors.Join(groupErr, workerErr)
}

func (h *cloudMediumRecipientFeedbackHarness) stopGroup(ctx context.Context) error {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.groupClosed || h.group == nil {
		return nil
	}
	if err := h.group.Stop(ctx); err != nil {
		return err
	}
	h.groupClosed = true
	return nil
}

func (h *cloudMediumRecipientFeedbackHarness) stopWorker(ctx context.Context) error {
	h.closeMu.Lock()
	defer h.closeMu.Unlock()
	if h.workerClosed || h.worker == nil {
		return nil
	}
	if err := h.worker.Stop(ctx); err != nil {
		return err
	}
	h.workerClosed = true
	return nil
}

func (h *cloudMediumRecipientFeedbackHarness) snapshot() cloudMediumRecipientFeedbackSnapshot {
	snapshot := h.observer.snapshot()
	snapshot.subscriberPages = int(h.source.pageCalls.Load())
	snapshot.subscriberRows = int(h.source.rows.Load())
	snapshot.authorityBatchCalls = int(h.node.authorityBatchCalls.Load())
	snapshot.authorityRows = int(h.node.authorityRows.Load())
	snapshot.routeRefreshCalls = int(h.node.routeRefreshCalls.Load())
	snapshot.remoteRPCCalls = int(h.node.remoteRPCCalls.Load())
	snapshot.onlineWrites = int(h.writes.Load())
	snapshot.pendingACKs = h.delivery.PendingAckCount()
	snapshot.directoryActive = h.directory.Snapshot().Active
	snapshot.workerCapacity = h.worker.WorkerCapacity()
	return snapshot
}

type cloudMediumRecipientFeedbackSnapshot struct {
	appendFinished      int
	appendErrors        int
	admissionAccepted   int
	admissionOther      int
	processedOK         int
	processedOther      int
	processedRecipients int
	postCommitFailures  int
	queueDepth          int
	queueCapacity       int
	maxQueueDepth       int
	inflight            int
	inflightCapacity    int
	maxInflight         int
	subscriberPages     int
	subscriberRows      int
	authorityBatchCalls int
	authorityRows       int
	routeRefreshCalls   int
	remoteRPCCalls      int
	onlineWrites        int
	pendingACKs         int
	directoryActive     int
	workerCapacity      int
}

func (s cloudMediumRecipientFeedbackSnapshot) validate() error {
	checks := []struct {
		name string
		got  int
		want int
	}{
		{name: "append finished", got: s.appendFinished, want: cloudMediumFeedbackMessageCount},
		{name: "append errors", got: s.appendErrors, want: 0},
		{name: "accepted plans", got: s.admissionAccepted, want: cloudMediumFeedbackPlanCount},
		{name: "other admissions", got: s.admissionOther, want: 0},
		{name: "processed plans", got: s.processedOK, want: cloudMediumFeedbackPlanCount},
		{name: "other process results", got: s.processedOther, want: 0},
		{name: "processed recipient rows", got: s.processedRecipients, want: cloudMediumFeedbackRecipientRows},
		{name: "post-commit failures", got: s.postCommitFailures, want: 0},
		{name: "final queue depth", got: s.queueDepth, want: 0},
		{name: "final inflight", got: s.inflight, want: 0},
		{name: "queue capacity", got: s.queueCapacity, want: 512},
		{name: "inflight capacity", got: s.inflightCapacity, want: cloudMediumFeedbackRecipientWorkers},
		{name: "worker capacity", got: s.workerCapacity, want: cloudMediumFeedbackRecipientWorkers},
		{name: "group subscriber pages", got: s.subscriberPages, want: cloudMediumFeedbackGroupSubscriberPlans},
		{name: "group subscriber rows", got: s.subscriberRows, want: cloudMediumFeedbackGroupSubscriberRows},
		{name: "authority batch calls", got: s.authorityBatchCalls, want: cloudMediumFeedbackPlanCount},
		{name: "authority rows", got: s.authorityRows, want: cloudMediumFeedbackRecipientRows},
		{name: "route refresh calls", got: s.routeRefreshCalls, want: 0},
		{name: "remote RPC calls", got: s.remoteRPCCalls, want: 0},
		{name: "owner-local writes", got: s.onlineWrites, want: cloudMediumFeedbackOwnerWrites},
		{name: "pending ACKs", got: s.pendingACKs, want: 0},
		{name: "presence directory active routes", got: s.directoryActive, want: cloudMediumFeedbackOnlineRoutes},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("%s = %d, want %d", check.name, check.got, check.want)
		}
	}
	if s.maxQueueDepth < 0 || s.maxQueueDepth > s.queueCapacity {
		return fmt.Errorf("max queue depth = %d, want within [0,%d]", s.maxQueueDepth, s.queueCapacity)
	}
	if s.maxInflight <= 0 || s.maxInflight > s.inflightCapacity {
		return fmt.Errorf("max inflight = %d, want within [1,%d]", s.maxInflight, s.inflightCapacity)
	}
	return nil
}

type cloudMediumRecipientFeedbackObserver struct {
	mu                  sync.Mutex
	appendFinished      int
	appendErrors        int
	admissionAccepted   int
	admissionOther      int
	processedOK         int
	processedOther      int
	processedRecipients int
	postCommitFailures  int
	queueDepth          int
	queueCapacity       int
	maxQueueDepth       int
	inflight            int
	inflightCapacity    int
	maxInflight         int
	changed             chan struct{}
}

func newCloudMediumRecipientFeedbackObserver() *cloudMediumRecipientFeedbackObserver {
	return &cloudMediumRecipientFeedbackObserver{changed: make(chan struct{}, 1)}
}

func (o *cloudMediumRecipientFeedbackObserver) AppendFinished(_ string, err error, _ time.Duration) {
	o.mu.Lock()
	o.appendFinished++
	if err != nil {
		o.appendErrors++
	}
	o.mu.Unlock()
	o.signal()
}

func (o *cloudMediumRecipientFeedbackObserver) SetChannelAppendRecipientDeliveryQueue(event channelappend.RecipientDeliveryQueueObservation) {
	o.mu.Lock()
	o.queueDepth = event.QueueDepth
	o.queueCapacity = event.QueueCapacity
	if event.QueueDepth > o.maxQueueDepth {
		o.maxQueueDepth = event.QueueDepth
	}
	o.mu.Unlock()
	o.signal()
}

func (o *cloudMediumRecipientFeedbackObserver) SetChannelAppendRecipientDeliveryWorkerPressure(event channelappend.RecipientDeliveryWorkerPressureObservation) {
	o.mu.Lock()
	o.inflight = event.Inflight
	o.inflightCapacity = event.Capacity
	if event.Inflight > o.maxInflight {
		o.maxInflight = event.Inflight
	}
	o.mu.Unlock()
	o.signal()
}

func (o *cloudMediumRecipientFeedbackObserver) ObserveChannelAppendRecipientDeliveryAdmission(event channelappend.RecipientDeliveryAdmissionObservation) {
	o.mu.Lock()
	if event.Result == "accepted" {
		o.admissionAccepted++
	} else {
		o.admissionOther++
	}
	o.mu.Unlock()
	o.signal()
}

func (o *cloudMediumRecipientFeedbackObserver) ObserveChannelAppendRecipientDeliveryProcess(event channelappend.RecipientDeliveryProcessObservation) {
	o.mu.Lock()
	if event.Result == "ok" {
		o.processedOK++
	} else {
		o.processedOther++
	}
	o.processedRecipients += event.Recipients
	o.mu.Unlock()
	o.signal()
}

func (o *cloudMediumRecipientFeedbackObserver) ObserveChannelAppendPostCommitFailure(channelappend.PostCommitFailureObservation) {
	o.mu.Lock()
	o.postCommitFailures++
	o.mu.Unlock()
	o.signal()
}

func (o *cloudMediumRecipientFeedbackObserver) waitDrained(ctx context.Context, expectedProcesses int) error {
	for {
		snapshot := o.snapshot()
		processes := snapshot.processedOK + snapshot.processedOther
		if processes > expectedProcesses {
			return fmt.Errorf("processed plans = %d, want at most %d", processes, expectedProcesses)
		}
		if processes == expectedProcesses && snapshot.queueDepth == 0 && snapshot.inflight == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for recipient drain (processed=%d queue=%d inflight=%d): %w", processes, snapshot.queueDepth, snapshot.inflight, ctx.Err())
		case <-o.changed:
		}
	}
}

func (o *cloudMediumRecipientFeedbackObserver) snapshot() cloudMediumRecipientFeedbackSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return cloudMediumRecipientFeedbackSnapshot{
		appendFinished:      o.appendFinished,
		appendErrors:        o.appendErrors,
		admissionAccepted:   o.admissionAccepted,
		admissionOther:      o.admissionOther,
		processedOK:         o.processedOK,
		processedOther:      o.processedOther,
		processedRecipients: o.processedRecipients,
		postCommitFailures:  o.postCommitFailures,
		queueDepth:          o.queueDepth,
		queueCapacity:       o.queueCapacity,
		maxQueueDepth:       o.maxQueueDepth,
		inflight:            o.inflight,
		inflightCapacity:    o.inflightCapacity,
		maxInflight:         o.maxInflight,
	}
}

func (o *cloudMediumRecipientFeedbackObserver) signal() {
	select {
	case o.changed <- struct{}{}:
	default:
	}
}

type cloudMediumRecipientFeedbackSubscribers struct {
	byChannel map[string][]channelappend.Recipient
	pageCalls atomic.Int64
	rows      atomic.Int64
}

func (s *cloudMediumRecipientFeedbackSubscribers) NextSubscriberPage(_ context.Context, request channelappend.SubscriberPageRequest) (channelappend.SubscriberPage, error) {
	recipients, ok := s.byChannel[request.ChannelID.ID]
	if !ok {
		return channelappend.SubscriberPage{}, fmt.Errorf("feedback subscribers missing channel %q", request.ChannelID.ID)
	}
	start := 0
	if request.Cursor != "" {
		parsed, err := strconv.Atoi(request.Cursor)
		if err != nil || parsed < 0 || parsed > len(recipients) {
			return channelappend.SubscriberPage{}, fmt.Errorf("feedback subscriber cursor %q is invalid", request.Cursor)
		}
		start = parsed
	}
	limit := request.Limit
	if limit <= 0 {
		return channelappend.SubscriberPage{}, fmt.Errorf("feedback subscriber limit = %d, want positive", limit)
	}
	end := start + limit
	if end > len(recipients) {
		end = len(recipients)
	}
	page := channelappend.SubscriberPage{
		Recipients: append([]channelappend.Recipient(nil), recipients[start:end]...),
		Done:       end == len(recipients),
	}
	if !page.Done {
		page.Cursor = strconv.Itoa(end)
	}
	s.pageCalls.Add(1)
	s.rows.Add(int64(len(page.Recipients)))
	return page, nil
}

type cloudMediumRecipientFeedbackNode struct {
	router              *clusterrouting.Router
	authorityBatchCalls atomic.Int64
	authorityRows       atomic.Int64
	routeRefreshCalls   atomic.Int64
	remoteRPCCalls      atomic.Int64
}

func newCloudMediumRecipientFeedbackNode() (*cloudMediumRecipientFeedbackNode, error) {
	const nodeCount = 3
	nodes := make([]control.Node, nodeCount)
	peers := make([]uint64, nodeCount)
	for index := 0; index < nodeCount; index++ {
		nodeID := uint64(index + 1)
		nodes[index] = control.Node{
			NodeID: nodeID,
			Addr:   fmt.Sprintf("127.0.0.1:%d", 11001+index),
			Roles:  []control.Role{control.RoleData},
			Status: control.NodeAlive,
		}
		peers[index] = nodeID
	}
	slots := make([]control.SlotAssignment, cloudMediumFeedbackLogicalSlots)
	statuses := make([]clusterrouting.SlotStatus, cloudMediumFeedbackLogicalSlots)
	for index := range slots {
		slotID := uint32(index + 1)
		slots[index] = control.SlotAssignment{
			SlotID:          slotID,
			DesiredPeers:    append([]uint64(nil), peers...),
			ConfigEpoch:     9,
			PreferredLeader: uint64(index%nodeCount + 1),
		}
		// The real observed Raft leader, not PreferredLeader, owns routing.
		// Keeping every observed leader local isolates the local pipeline and
		// makes any transport RPC an explicit test failure.
		statuses[index] = clusterrouting.SlotStatus{SlotID: slotID, Leader: 1, LeaderTerm: 7}
	}
	ranges := make([]control.HashSlotRange, cloudMediumFeedbackHashSlots)
	for hashSlot := range ranges {
		ranges[hashSlot] = control.HashSlotRange{
			From:   uint16(hashSlot),
			To:     uint16(hashSlot),
			SlotID: uint32(hashSlot%cloudMediumFeedbackLogicalSlots + 1),
		}
	}
	router := clusterrouting.NewRouter()
	if err := router.UpdateControlSnapshot(control.Snapshot{
		Revision:     cloudMediumFeedbackRouteRevision,
		ControllerID: 1,
		Nodes:        nodes,
		Slots:        slots,
		HashSlots: control.HashSlotTable{
			Revision: cloudMediumFeedbackRouteRevision,
			Count:    cloudMediumFeedbackHashSlots,
			Ranges:   ranges,
		},
	}); err != nil {
		return nil, fmt.Errorf("install feedback route snapshot: %w", err)
	}
	router.UpdateSlotLeaders(statuses)
	return &cloudMediumRecipientFeedbackNode{router: router}, nil
}

func (n *cloudMediumRecipientFeedbackNode) NodeID() uint64 { return 1 }

func (n *cloudMediumRecipientFeedbackNode) RouteKey(key string) (clusterpkg.Route, error) {
	route, err := n.router.RouteKey(key)
	return cloudMediumClusterRoute(route), err
}

func (n *cloudMediumRecipientFeedbackNode) RouteKeysPartial(keys []string) ([]clusterpkg.RouteKeyResult, error) {
	n.routeRefreshCalls.Add(1)
	routes, err := n.router.RouteKeysPartial(keys)
	if err != nil {
		return nil, err
	}
	results := make([]clusterpkg.RouteKeyResult, len(routes))
	for i, result := range routes {
		results[i].Err = result.Err
		results[i].Route = cloudMediumClusterRoute(result.Route)
	}
	return results, nil
}

func (n *cloudMediumRecipientFeedbackNode) RouteAuthoritiesPartial(keys []string) ([]clusterpkg.RouteAuthorityResult, error) {
	n.authorityBatchCalls.Add(1)
	n.authorityRows.Add(int64(len(keys)))
	results, err := n.router.RouteAuthoritiesPartial(keys)
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].Authority.AuthorityEpoch = cloudMediumFeedbackAuthorityEpoch
	}
	return results, nil
}

func (n *cloudMediumRecipientFeedbackNode) RouteHashSlot(hashSlot uint16) (clusterpkg.Route, error) {
	route, err := n.router.RouteHashSlot(hashSlot)
	return cloudMediumClusterRoute(route), err
}

func (n *cloudMediumRecipientFeedbackNode) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	n.remoteRPCCalls.Add(1)
	return nil, errors.New("cloud medium local feedback unexpectedly attempted remote RPC")
}

func (n *cloudMediumRecipientFeedbackNode) RegisterRPC(uint8, clusterpkg.NodeRPCHandler) {}

func (n *cloudMediumRecipientFeedbackNode) WatchRouteAuthorities() <-chan clusterpkg.RouteAuthorityEvent {
	return nil
}

func cloudMediumClusterRoute(route clusterrouting.Route) clusterpkg.Route {
	return clusterpkg.Route{
		HashSlot:        route.HashSlot,
		SlotID:          route.SlotID,
		Leader:          route.Leader,
		LeaderTerm:      route.LeaderTerm,
		ConfigEpoch:     route.ConfigEpoch,
		PreferredLeader: route.PreferredLeader,
		Peers:           append([]uint64(nil), route.Peers...),
		Revision:        route.Revision,
		AuthorityEpoch:  cloudMediumFeedbackAuthorityEpoch,
	}
}

func cloudMediumPresenceTarget(route clusterpkg.Route) authoritypresence.RouteTarget {
	return authoritypresence.RouteTarget{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		LeaderTerm:     route.LeaderTerm,
		ConfigEpoch:    route.ConfigEpoch,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}

type cloudMediumRecipientFeedbackMessageIDs struct {
	next atomic.Uint64
}

func (ids *cloudMediumRecipientFeedbackMessageIDs) Next() uint64 {
	return ids.next.Add(1)
}

type cloudMediumRecipientFeedbackAppender struct {
	sequence atomic.Uint64
}

func (a *cloudMediumRecipientFeedbackAppender) AppendBatch(ctx context.Context, request channelappend.AppendBatchRequest) (channelappend.AppendBatchResult, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return channelappend.AppendBatchResult{}, err
		}
	}
	result := channelappend.AppendBatchResult{Items: make([]channelappend.AppendBatchItemResult, len(request.Messages))}
	for i, message := range request.Messages {
		sequence := a.sequence.Add(1)
		accepted := message.Clone()
		accepted.MessageSeq = sequence
		result.Items[i] = channelappend.AppendBatchItemResult{
			MessageID:  message.MessageID,
			MessageSeq: sequence,
			Message:    accepted,
		}
	}
	return result, nil
}

type cloudMediumRecipientFeedbackSession struct {
	uid       string
	sessionID uint64
	delivery  *runtimedelivery.Manager
	writes    *atomic.Int64
}

func (s *cloudMediumRecipientFeedbackSession) WriteDelivery(value any) error {
	packet, ok := value.(*frame.RecvPacket)
	if !ok || packet == nil {
		return fmt.Errorf("feedback session delivery packet type = %T, want *frame.RecvPacket", value)
	}
	if packet.MessageID <= 0 {
		return fmt.Errorf("feedback session message id = %d, want positive", packet.MessageID)
	}
	s.writes.Add(1)
	return s.delivery.Recvack(context.Background(), runtimedelivery.Recvack{
		UID:        s.uid,
		SessionID:  s.sessionID,
		MessageID:  uint64(packet.MessageID),
		MessageSeq: packet.MessageSeq,
	})
}

func (*cloudMediumRecipientFeedbackSession) CloseSession(string) error { return nil }
