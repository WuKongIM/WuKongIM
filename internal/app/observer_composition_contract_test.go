package app

import (
	"errors"
	"testing"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internal/access/api"
	gatewayadapter "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	clustertasks "github.com/WuKongIM/WuKongIM/pkg/cluster/tasks"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
	messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	gateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestCompositeChannelObserverPreservesEveryOptionalRuntimeSignal(t *testing.T) {
	t.Parallel()
	first := newChannelCompositionProbe()
	second := newChannelCompositionProbe()
	observer := multiChannelObserver{first, second}
	errSentinel := errors.New("worker failed")

	observer.SetReactorMailboxDepth(1, "high", 2)
	observer.SetReactorMailboxCapacity(1, "high", 8)
	observer.ObserveReactorMailboxAdmission(1, "high", "ok")
	observer.SetAppendQueuePressure(reactor.AppendQueuePressureEvent{ReactorID: 1, Depth: 2})
	observer.SetWorkerQueueDepth("store", 3)
	observer.SetWorkerQueueCapacity("store", 8)
	observer.SetWorkerWorkers("store", 2)
	observer.ObserveWorkerAdmission("store", "ok")
	observer.ObserveWorkerAdmissionKind("store", worker.TaskStoreAppend, "ok")
	observer.ObserveWorkerWait("store", worker.TaskStoreAppend, time.Millisecond)
	observer.ObserveWorkerTask("store", worker.TaskStoreAppend, errSentinel, time.Millisecond)
	observer.ObserveWorkerBatch("store", worker.TaskStoreAppend, 4, errSentinel)
	observer.SetWorkerInflight("store", 2)
	observer.SetWorkerInflightPeak("store", 4)
	observer.SetWorkerAntsPoolUsage("store", 2, 8, 1)
	observer.ObserveChannelActivationRejected("max_channels")
	observer.SetFollowerParkedCount(1, 2)
	observer.ObserveFollowerRecoveryProbe("ok")
	observer.ObservePull("ok", false)
	observer.ObservePullHintResult(channeltransport.PullHintReasonAppend, "ok", nil)
	observer.ObservePullHintReceived(channeltransport.PullHintReasonResume, "apply", nil)
	observer.SetPendingMetaCount(1, 2)
	observer.ObservePendingMeta("resolved", nil)
	observer.ObserveNeedMetaPull("ok", nil)
	observer.ObserveReplicationStage("send", "ok", time.Millisecond)
	observer.ObserveChannelMetaCache("hit")
	observer.ObserveAppendBatch(2, 128, time.Millisecond)
	observer.ObserveAppendLatency(ch.CommitModeQuorum, time.Millisecond)
	observer.ObserveConversationHydrationBatch("ok", 2, 1, 1, time.Millisecond)
	observer.ObserveAppendWaitStage("quorum", ch.CommitModeQuorum, "ok", time.Millisecond)
	observer.ObserveAppendWaitCanceled(reactor.AppendWaitCancelSnapshot{})
	observer.ObserveWorkerResult(worker.TaskStoreAppend, errSentinel, time.Millisecond)

	want := []string{
		"mailbox_depth", "mailbox_capacity", "mailbox_admission", "append_pressure",
		"worker_depth", "worker_capacity", "worker_count", "worker_admission",
		"worker_kind_admission", "worker_wait", "worker_task", "worker_batch",
		"worker_inflight", "worker_peak", "worker_ants", "activation_rejected",
		"follower_parked", "follower_probe", "pull", "pull_hint_result",
		"pull_hint_received", "pending_meta_count", "pending_meta", "need_meta_pull",
		"replication_stage", "meta_cache", "append_batch", "append_latency",
		"conversation_hydration", "append_wait", "append_cancel", "worker_result",
	}
	for index, probe := range []*channelCompositionProbe{first, second} {
		for _, name := range want {
			if probe.calls[name] != 1 {
				t.Fatalf("child %d callback %q count = %d, want 1", index, name, probe.calls[name])
			}
		}
	}
}

func TestCompositeGatewayAndSlotObserversPreserveExtendedSignals(t *testing.T) {
	t.Parallel()
	firstGateway := newGatewayCompositionProbe()
	secondGateway := newGatewayCompositionProbe()
	gatewayObserver := multiGatewayObserver{firstGateway, secondGateway}
	gatewayObserver.OnConnectionOpen(gateway.ConnectionEvent{})
	gatewayObserver.OnConnectionClose(gateway.ConnectionEvent{})
	gatewayObserver.OnAuth(gateway.AuthEvent{})
	gatewayObserver.OnFrameIn(gateway.FrameEvent{})
	gatewayObserver.OnFrameOut(gateway.FrameEvent{})
	gatewayObserver.OnFrameHandled(gateway.FrameHandleEvent{})
	gatewayObserver.OnTransportWrite(gateway.TransportWriteEvent{})
	gatewayObserver.OnSessionError(gateway.SessionErrorEvent{})
	gatewayObserver.OnAsyncSendQueue(gateway.AsyncSendQueueEvent{})
	gatewayObserver.OnAsyncSendAdmission(gateway.AsyncSendAdmissionEvent{})
	gatewayObserver.OnAsyncSendBatch(gateway.AsyncSendBatchEvent{})
	gatewayObserver.OnAsyncSendDispatchWait(gateway.AsyncSendDispatchWaitEvent{})
	gatewayObserver.OnAsyncAuthQueue(gateway.AsyncAuthQueueEvent{})
	gatewayObserver.OnAsyncAuthAdmission(gateway.AsyncAuthAdmissionEvent{})
	gatewayObserver.OnAsyncAuthWait(gateway.AsyncAuthWaitEvent{})
	gatewayObserver.OnTransportPressure(gateway.TransportPressureEvent{})
	multiSendackObserver{firstGateway, secondGateway}.SendackWritten(gatewayadapter.SendackEvent{})
	for index, probe := range []*gatewayCompositionProbe{firstGateway, secondGateway} {
		if probe.calls != 17 {
			t.Fatalf("gateway child %d calls = %d, want 17", index, probe.calls)
		}
	}

	firstSlot := &slotCompositionProbe{}
	secondSlot := &slotCompositionProbe{}
	slotObserver := multiSlotObserver{firstSlot, secondSlot}
	slotObserver.SetSchedulerWorkers(2)
	slotObserver.SetSchedulerInflight(1)
	slotObserver.SetSchedulerState(multiraft.SchedulerStateEvent{})
	slotObserver.ObserveSchedulerAdmission("ok")
	slotObserver.ObserveSchedulerTask("advance", time.Millisecond)
	slotObserver.ObserveSlotProposal(7, time.Millisecond)
	slotObserver.ObserveSlotProposalAdmission(7, multiraft.ProposalClassForeground, "ok")
	slotObserver.ObserveSlotLeaderChange(7, 1, 2)
	slotObserver.ObserveSlotLeaderChangeWithCause(7, 2, 3, multiraft.LeaderChangeCausePlannedTransfer)
	slotObserver.SetSlotApplyState(7, 9, 8)
	for index, probe := range []*slotCompositionProbe{firstSlot, secondSlot} {
		if probe.calls != 10 {
			t.Fatalf("slot child %d calls = %d, want 10", index, probe.calls)
		}
	}
}

func TestCompositeCrossLayerObserversFanOutAndKeepNilCapabilitiesExplicit(t *testing.T) {
	t.Parallel()
	transportFirst, transportSecond := &transportCompositionProbe{}, &transportCompositionProbe{}
	if got := combineTransportObservers(nil, transportFirst); got != transportFirst {
		t.Fatal("nil first transport observer was not treated as optional")
	}
	if got := combineTransportObservers(transportFirst, nil); got != transportFirst {
		t.Fatal("nil second transport observer was not treated as optional")
	}
	combinedTransport := combineTransportObservers(transportFirst, transportSecond)
	combinedTransport.ObserveTransport(transport.Event{Name: "sent_bytes"})
	if transportFirst.calls != 1 || transportSecond.calls != 1 {
		t.Fatalf("transport fanout = %d/%d", transportFirst.calls, transportSecond.calls)
	}

	controllerFirst, controllerSecond := &controllerCompositionProbe{}, &controllerCompositionProbe{}
	if combineControllerRaftObservers(nil, controllerFirst) != controllerFirst || combineControllerRaftObservers(controllerFirst, nil) != controllerFirst {
		t.Fatal("controller observer nil capability handling changed")
	}
	combinedController := combineControllerRaftObservers(controllerFirst, controllerSecond).(multiControllerRaftObserver)
	combinedController.SetStepQueueDepth(1, 2)
	combinedController.ObserveStepEnqueue("ok", time.Millisecond)
	combinedController.SetApplyState(2, 1)
	if controllerFirst.calls != 3 || controllerSecond.calls != 3 {
		t.Fatalf("controller fanout = %d/%d", controllerFirst.calls, controllerSecond.calls)
	}

	controlFirst, controlSecond := &controlSnapshotCompositionProbe{}, &controlSnapshotCompositionProbe{}
	if combineControlSnapshotObservers(nil, controlFirst) != controlFirst || combineControlSnapshotObservers(controlFirst, nil) != controlFirst {
		t.Fatal("control snapshot nil capability handling changed")
	}
	combineControlSnapshotObservers(controlFirst, controlSecond).ObserveControlSnapshot(control.Snapshot{})
	if controlFirst.calls != 1 || controlSecond.calls != 1 {
		t.Fatalf("control snapshot fanout = %d/%d", controlFirst.calls, controlSecond.calls)
	}

	moveFirst, moveSecond := &slotMoveCompositionProbe{}, &slotMoveCompositionProbe{}
	if combineSlotReplicaMoveObservers(nil, moveFirst) != moveFirst || combineSlotReplicaMoveObservers(moveFirst, nil) != moveFirst {
		t.Fatal("slot move nil capability handling changed")
	}
	combineSlotReplicaMoveObservers(moveFirst, moveSecond).ObserveSlotReplicaMovePhase("copy", "ok", time.Millisecond)
	if moveFirst.calls != 1 || moveSecond.calls != 1 {
		t.Fatalf("slot move fanout = %d/%d", moveFirst.calls, moveSecond.calls)
	}

	preferredFirst, preferredSecond := &preferredCompositionProbe{}, &preferredCompositionProbe{}
	if combinePreferredLeaderObservers(nil, preferredFirst) != preferredFirst || combinePreferredLeaderObservers(preferredFirst, nil) != preferredFirst {
		t.Fatal("preferred leader nil capability handling changed")
	}
	combinedPreferred := combinePreferredLeaderObservers(preferredFirst, preferredSecond).(multiPreferredLeaderObserver)
	combinedPreferred.ObservePreferredLeaderDecision("match")
	combinedPreferred.ObservePreferredLeaderStrictWait("match", time.Millisecond)
	combinedPreferred.ObservePreferredLeaderReconcile(clustertasks.PreferredLeaderObservation{SlotID: 7})
	if preferredFirst.calls != 3 || preferredSecond.calls != 3 {
		t.Fatalf("preferred leader fanout = %d/%d", preferredFirst.calls, preferredSecond.calls)
	}

	commitFirst, commitSecond := &commitCompositionProbe{}, &commitCompositionProbe{}
	if combineCommitCoordinatorObservers(nil, commitFirst) != commitFirst || combineCommitCoordinatorObservers(commitFirst, nil) != commitFirst {
		t.Fatal("commit coordinator nil capability handling changed")
	}
	combinedCommit := combineCommitCoordinatorObservers(commitFirst, commitSecond).(multiCommitCoordinatorObserver)
	combinedCommit.SetCommitCoordinatorQueueDepth(2)
	combinedCommit.SetCommitCoordinatorQueue(2, 8)
	combinedCommit.ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent{})
	combinedCommit.ObserveCommitCoordinatorRequest(messagedb.CommitCoordinatorRequestEvent{})
	if commitFirst.calls != 4 || commitSecond.calls != 4 {
		t.Fatalf("commit coordinator fanout = %d/%d", commitFirst.calls, commitSecond.calls)
	}

	messageFirst, messageSecond := &messageEventCompositionProbe{}, &messageEventCompositionProbe{}
	if combineMessageEventObservers(nil, messageFirst) != messageFirst || combineMessageEventObservers(messageFirst, nil) != messageFirst {
		t.Fatal("message event nil capability handling changed")
	}
	combinedMessage := combineMessageEventObservers(messageFirst, messageSecond).(multiMessageEventObserver)
	combinedMessage.ObserveMessageEventAppend(cluster.MessageEventAppendObservation{})
	combinedMessage.ObserveMessageEventAppendStage(cluster.MessageEventAppendStageObservation{})
	combinedMessage.ObserveMessageEventPropose(cluster.MessageEventProposeObservation{})
	combinedMessage.ObserveMessageEventProposeStage(cluster.MessageEventProposeStageObservation{})
	combinedMessage.SetMessageEventStreamCache(cluster.MessageEventStreamCacheObservation{})
	if messageFirst.calls != 5 || messageSecond.calls != 5 {
		t.Fatalf("message event fanout = %d/%d", messageFirst.calls, messageSecond.calls)
	}

	membershipFirst, membershipSecond := &membershipCompositionProbe{}, &membershipCompositionProbe{}
	if combineMembershipMutationObservers(nil, membershipFirst) != membershipFirst || combineMembershipMutationObservers(membershipFirst, nil) != membershipFirst {
		t.Fatal("membership nil capability handling changed")
	}
	combineMembershipMutationObservers(membershipFirst, membershipSecond).ObserveMembershipMutation(cluster.MembershipMutationObservation{})
	if membershipFirst.calls != 1 || membershipSecond.calls != 1 {
		t.Fatalf("membership fanout = %d/%d", membershipFirst.calls, membershipSecond.calls)
	}

	channel := &channelCompositionProbe{calls: make(map[string]int)}
	if combineChannelObservers(nil, channel) != channel || combineChannelObservers(channel, nil) != channel {
		t.Fatal("channel nil capability handling changed")
	}
	slot := &slotCompositionProbe{}
	if combineSlotObservers(nil, slot) != slot || combineSlotObservers(slot, nil) != slot {
		t.Fatal("slot nil capability handling changed")
	}
}

func TestMetricsAdaptersAcceptOptionalSignalsWithoutIdentityLabels(t *testing.T) {
	t.Parallel()
	registry := obsmetrics.New(1, "node-1")
	gatewayObserver := gatewayMetricsObserver{metrics: registry}
	gatewayObserver.OnConnectionOpen(gateway.ConnectionEvent{Protocol: "wkproto"})
	gatewayObserver.OnConnectionClose(gateway.ConnectionEvent{Protocol: "wkproto"})
	gatewayObserver.OnAuth(gateway.AuthEvent{Status: "ok", Duration: time.Millisecond})
	gatewayObserver.OnFrameIn(gateway.FrameEvent{ConnectionEvent: gateway.ConnectionEvent{Protocol: "wkproto"}, FrameType: "SEND", Bytes: 32})
	gatewayObserver.OnFrameOut(gateway.FrameEvent{ConnectionEvent: gateway.ConnectionEvent{Protocol: "wkproto"}, FrameType: "RECV", Bytes: 64})
	gatewayObserver.OnFrameHandled(gateway.FrameHandleEvent{FrameType: "SEND", Duration: time.Millisecond})
	gatewayObserver.OnTransportWrite(gateway.TransportWriteEvent{FrameType: "SEND", Duration: time.Millisecond})
	gatewayObserver.OnAsyncSendBatch(gateway.AsyncSendBatchEvent{Records: 2, Bytes: 96, Wait: time.Millisecond})
	gatewayObserver.SendackWritten(gatewayadapter.SendackEvent{Reason: messageusecase.ReasonSuccess, Source: "gateway"})

	channelObserver := channelMetricsObserver{metrics: registry}
	channelObserver.ObserveWorkerAdmissionKind("store", worker.TaskStoreAppend, "ok")
	channelObserver.SetWorkerInflightPeak("store", 3)
	channelObserver.SetChannelRuntimeCount(1, ch.RoleLeader, 2)
	channelObserver.ObserveChannelActivationRejected("max_channels")
	channelObserver.SetFollowerParkedCount(1, 2)
	channelObserver.ObserveFollowerRecoveryProbe("ok")
	channelObserver.ObservePull("ok", false)
	channelObserver.ObservePullHintResult(channeltransport.PullHintReasonAppend, "ok", nil)
	channelObserver.ObservePullHintReceived(channeltransport.PullHintReasonResume, "apply", nil)
	channelObserver.SetPendingMetaCount(1, 2)
	channelObserver.ObservePendingMeta("resolved", nil)
	channelObserver.ObserveNeedMetaPull("ok", nil)
	channelObserver.ObserveReplicationStage("send", "ok", time.Millisecond)
	channelObserver.ObserveChannelMetaCache("hit")
	channelObserver.ObserveAppendBatch(2, 128, time.Millisecond)
	channelObserver.ObserveAppendLatency(ch.CommitModeQuorum, time.Millisecond)
	channelObserver.ObserveConversationHydrationBatch("ok", 2, 1, 1, time.Millisecond)
	channelObserver.ObserveAppendWaitStage("quorum", ch.CommitModeQuorum, "ok", time.Millisecond)
	channelObserver.ObserveAppendWaitCanceled(reactor.AppendWaitCancelSnapshot{})
	channelObserver.ObserveWorkerResult(worker.TaskStoreAppend, nil, time.Millisecond)

	slotObserver := slotMetricsObserver{metrics: registry}
	slotObserver.ObserveSlotLeaderChange(7, 1, 2)
	slotObserver.ObservePreferredLeaderDecision("match")
	slotObserver.ObservePreferredLeaderStrictWait("match", time.Millisecond)
	controllerObserver := controllerRaftMetricsObserver{metrics: registry}
	controllerObserver.SetStepQueueDepth(1, 8)
	controllerObserver.ObserveStepEnqueue("ok", time.Millisecond)
	(&nodeLifecycleMetricsObserver{metrics: registry}).ObserveNodeLifecycleAttempt("join", "ok")
	storageCommitMetricsObserver{metrics: registry}.SetCommitCoordinatorQueueDepth(1)
	messageObserver := messageEventMetricsObserver{metrics: registry}
	messageObserver.ObserveMessageEventAppend(cluster.MessageEventAppendObservation{Path: "local", EventType: "message", Result: "ok", Duration: time.Millisecond})
	messageObserver.ObserveMessageEventAppendStage(cluster.MessageEventAppendStageObservation{Path: "local", Stage: "commit", Result: "ok", Duration: time.Millisecond})
	messageObserver.ObserveMessageEventPropose(cluster.MessageEventProposeObservation{Path: "local", Result: "ok", BatchSize: 2, Duration: time.Millisecond})
	messageObserver.ObserveMessageEventProposeStage(cluster.MessageEventProposeStageObservation{Path: "local", Stage: "propose", Result: "ok", Duration: time.Millisecond})
	messageObserver.SetMessageEventStreamCache(cluster.MessageEventStreamCacheObservation{Sessions: 1, OpenLanes: 1, PayloadBytes: 64, MaxSessions: 8})
	deliveryMetricsObserver{metrics: registry}.ObserveRecipientAuthorityResolve(clusterinfra.RecipientAuthorityResolveObservation{Result: "ok", Items: 2, Targets: 1, Duration: time.Millisecond})
	presenceMetricsObserver{metrics: registry}.ObservePresenceEndpointLookup(clusterinfra.PresenceEndpointLookupObservation{Path: "local", Outcome: "hit", Items: 2, Groups: 1, Duration: time.Millisecond})

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, name := range []string{
		"wukongim_gateway_connections_total",
		"wukongim_channelv2_worker_admission_total",
		"wukongim_message_event_append_total",
		"wukongim_delivery_recipient_authority_resolve_total",
		"wukongim_presence_endpoint_lookup_total",
	} {
		if requireAppMetricFamily(t, families, name) == nil {
			t.Fatalf("metric family %q missing", name)
		}
	}
}

func TestDisabledMetricsAdaptersRemainSafeNoopsForEveryOptionalCapability(t *testing.T) {
	t.Parallel()
	gatewayObserver := gatewayMetricsObserver{}
	gatewayObserver.OnConnectionOpen(gateway.ConnectionEvent{})
	gatewayObserver.OnConnectionClose(gateway.ConnectionEvent{})
	gatewayObserver.OnAuth(gateway.AuthEvent{})
	gatewayObserver.OnFrameIn(gateway.FrameEvent{})
	gatewayObserver.OnFrameOut(gateway.FrameEvent{})
	gatewayObserver.OnFrameHandled(gateway.FrameHandleEvent{})
	gatewayObserver.OnTransportWrite(gateway.TransportWriteEvent{})
	gatewayObserver.OnAsyncSendQueue(gateway.AsyncSendQueueEvent{})
	gatewayObserver.OnAsyncSendAdmission(gateway.AsyncSendAdmissionEvent{})
	gatewayObserver.OnAsyncSendBatch(gateway.AsyncSendBatchEvent{})
	gatewayObserver.OnAsyncSendDispatchWait(gateway.AsyncSendDispatchWaitEvent{})
	gatewayObserver.OnAsyncAuthQueue(gateway.AsyncAuthQueueEvent{})
	gatewayObserver.OnAsyncAuthAdmission(gateway.AsyncAuthAdmissionEvent{})
	gatewayObserver.OnAsyncAuthWait(gateway.AsyncAuthWaitEvent{})
	gatewayObserver.OnTransportPressure(gateway.TransportPressureEvent{})
	gatewayObserver.SendackWritten(gatewayadapter.SendackEvent{})

	conversationListMetricsObserver{}.ObserveConversationList(accessapi.ConversationListObservation{})

	channelObserver := channelMetricsObserver{}
	channelObserver.SetReactorMailboxDepth(0, "high", 0)
	channelObserver.SetReactorMailboxCapacity(0, "high", 0)
	channelObserver.ObserveReactorMailboxAdmission(0, "high", "ok")
	channelObserver.SetAppendQueuePressure(reactor.AppendQueuePressureEvent{})
	channelObserver.SetWorkerQueueDepth("worker", 0)
	channelObserver.SetWorkerQueueCapacity("worker", 0)
	channelObserver.SetWorkerWorkers("worker", 0)
	channelObserver.ObserveWorkerAdmission("worker", "ok")
	channelObserver.ObserveWorkerAdmissionKind("worker", worker.TaskFunc, "ok")
	channelObserver.ObserveWorkerWait("worker", worker.TaskFunc, 0)
	channelObserver.ObserveWorkerTask("worker", worker.TaskFunc, nil, 0)
	channelObserver.ObserveWorkerBatch("worker", worker.TaskFunc, 0, nil)
	channelObserver.SetWorkerInflight("worker", 0)
	channelObserver.SetWorkerInflightPeak("worker", 0)
	channelObserver.SetWorkerAntsPoolUsage("worker", 0, 0, 0)
	channelObserver.SetChannelRuntimeCount(0, ch.RoleFollower, 0)
	channelObserver.ObserveRuntimeLoad(ch.RoleFollower)
	channelObserver.ObserveRuntimeEviction(ch.RoleFollower, reactor.RuntimeEvictionReasonIdle)
	channelObserver.ObserveChannelActivationRejected("disabled")
	channelObserver.SetFollowerParkedCount(0, 0)
	channelObserver.ObserveFollowerRecoveryProbe("disabled")
	channelObserver.ObservePull("disabled", true)
	channelObserver.ObservePullBatch(ch.PullBatchObservation{})
	channelObserver.ObserveLeaderPullStage(0, "disabled", 0)
	channelObserver.ObserveLeaderPullCompletedWaiters(0, 0)
	channelObserver.ObservePullHintResult(channeltransport.PullHintReasonAppend, "disabled", nil)
	channelObserver.ObservePullHintReceived(channeltransport.PullHintReasonAppend, "disabled", nil)
	channelObserver.SetPendingMetaCount(0, 0)
	channelObserver.ObservePendingMeta("disabled", nil)
	channelObserver.ObserveNeedMetaPull("disabled", nil)
	channelObserver.ObserveReplicationStage("disabled", "disabled", 0)
	channelObserver.ObserveChannelMetaCache("disabled")
	channelObserver.ObserveChannelMetaCreate(0, clusterchannels.MetaCreateResult("disabled"))
	channelObserver.SetChannelMetaCreateQueueDepth(0, 0)
	channelObserver.ObserveChannelMetaCreateCoalesced(0)
	channelObserver.ObserveChannelMetaCreateBatch(0, "disabled", 0)
	channelObserver.ObserveAppendBatch(0, 0, 0)
	channelObserver.ObserveAppendLatency(ch.CommitModeLocal, 0)
	channelObserver.ObserveChannelAppendStage("disabled", "disabled", 0)
	channelObserver.ObserveConversationHydrationBatch("disabled", 0, 0, 0, 0)
	channelObserver.ObserveAppendWaitStage("disabled", ch.CommitModeLocal, "disabled", 0)
	channelObserver.ObserveAppendWaitCanceled(reactor.AppendWaitCancelSnapshot{})
	channelObserver.ObserveWorkerResult(worker.TaskFunc, nil, 0)

	slotObserver := slotMetricsObserver{}
	slotObserver.SetSchedulerWorkers(0)
	slotObserver.SetSchedulerInflight(0)
	slotObserver.SetSchedulerState(multiraft.SchedulerStateEvent{})
	slotObserver.ObserveSchedulerAdmission("disabled")
	slotObserver.ObserveSchedulerTask("disabled", 0)
	slotObserver.ObserveSlotProposal(0, 0)
	slotObserver.ObserveSlotProposalAdmission(0, multiraft.ProposalClassForeground, "disabled")
	slotObserver.ObserveSlotLeaderChange(0, 0, 0)
	slotObserver.ObserveSlotLeaderChangeWithCause(0, 0, 0, multiraft.LeaderChangeCauseElection)
	slotObserver.SetSlotApplyState(0, 0, 0)
	slotObserver.ObservePreferredLeaderDecision("disabled")
	slotObserver.ObservePreferredLeaderStrictWait("disabled", 0)

	preferred := &preferredLeaderDiagnosticsObserver{}
	preferred.ObservePreferredLeaderDecision("disabled")
	preferred.ObservePreferredLeaderStrictWait("disabled", 0)
	preferred.ObservePreferredLeaderReconcile(clustertasks.PreferredLeaderObservation{})
	(&transportMetricsObserver{}).ObserveTransport(transport.Event{})
	controllerObserver := controllerRaftMetricsObserver{}
	controllerObserver.SetStepQueueDepth(0, 0)
	controllerObserver.ObserveStepEnqueue("disabled", 0)
	controllerObserver.SetApplyState(0, 0)
	controllerObserver.ObserveControllerRaftStatus(managementusecase.ControllerRaftStatus{})
	controllerObserver.ObserveControllerVoterPromotionAttempt("disabled")
	controllerObserver.ObserveControllerVoterPromotionBlocker("disabled")
	controllerObserver.ObserveControllerVoterPromotionPhase("disabled", 0)
	controlSnapshotMetricsObserver{}.ObserveControlSnapshot(control.Snapshot{})
	(&nodeLifecycleMetricsObserver{}).ObserveNodeLifecycleAttempt("disabled", "disabled")
	(&nodeLifecycleMetricsObserver{}).ObserveScaleInStatus(managementusecase.NodeScaleInStatusResponse{})
	commitObserver := storageCommitMetricsObserver{}
	commitObserver.SetCommitCoordinatorQueueDepth(0)
	commitObserver.SetCommitCoordinatorQueue(0, 0)
	commitObserver.ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent{})
	commitObserver.ObserveCommitCoordinatorRequest(messagedb.CommitCoordinatorRequestEvent{})
	messageObserver := messageEventMetricsObserver{}
	messageObserver.ObserveMessageEventAppend(cluster.MessageEventAppendObservation{})
	messageObserver.ObserveMessageEventAppendStage(cluster.MessageEventAppendStageObservation{})
	messageObserver.ObserveMessageEventPropose(cluster.MessageEventProposeObservation{})
	messageObserver.ObserveMessageEventProposeStage(cluster.MessageEventProposeStageObservation{})
	messageObserver.SetMessageEventStreamCache(cluster.MessageEventStreamCacheObservation{})
	membershipMutationMetricsObserver{}.ObserveMembershipMutation(cluster.MembershipMutationObservation{})
	deliveryMetricsObserver{}.ObserveRecipientAuthorityResolve(clusterinfra.RecipientAuthorityResolveObservation{})
	presenceObserver := presenceMetricsObserver{}
	presenceObserver.ObservePresenceEndpointLookup(clusterinfra.PresenceEndpointLookupObservation{})
}

type channelCompositionProbe struct {
	channelMetricsObserver
	calls map[string]int
}

func newChannelCompositionProbe() *channelCompositionProbe {
	return &channelCompositionProbe{calls: make(map[string]int)}
}

func (p *channelCompositionProbe) record(name string) { p.calls[name]++ }
func (p *channelCompositionProbe) SetReactorMailboxDepth(int, string, int) {
	p.record("mailbox_depth")
}
func (p *channelCompositionProbe) SetReactorMailboxCapacity(int, string, int) {
	p.record("mailbox_capacity")
}
func (p *channelCompositionProbe) ObserveReactorMailboxAdmission(int, string, string) {
	p.record("mailbox_admission")
}
func (p *channelCompositionProbe) SetAppendQueuePressure(reactor.AppendQueuePressureEvent) {
	p.record("append_pressure")
}
func (p *channelCompositionProbe) SetWorkerQueueDepth(string, int) {
	p.record("worker_depth")
}
func (p *channelCompositionProbe) SetWorkerQueueCapacity(string, int) {
	p.record("worker_capacity")
}
func (p *channelCompositionProbe) SetWorkerWorkers(string, int) { p.record("worker_count") }
func (p *channelCompositionProbe) ObserveWorkerAdmission(string, string) {
	p.record("worker_admission")
}
func (p *channelCompositionProbe) ObserveWorkerAdmissionKind(string, worker.TaskKind, string) {
	p.record("worker_kind_admission")
}
func (p *channelCompositionProbe) ObserveWorkerWait(string, worker.TaskKind, time.Duration) {
	p.record("worker_wait")
}
func (p *channelCompositionProbe) ObserveWorkerTask(string, worker.TaskKind, error, time.Duration) {
	p.record("worker_task")
}
func (p *channelCompositionProbe) ObserveWorkerBatch(string, worker.TaskKind, int, error) {
	p.record("worker_batch")
}
func (p *channelCompositionProbe) SetWorkerInflight(string, int) { p.record("worker_inflight") }
func (p *channelCompositionProbe) SetWorkerInflightPeak(string, int) {
	p.record("worker_peak")
}
func (p *channelCompositionProbe) SetWorkerAntsPoolUsage(string, int, int, int) {
	p.record("worker_ants")
}
func (p *channelCompositionProbe) ObserveChannelActivationRejected(string) {
	p.record("activation_rejected")
}
func (p *channelCompositionProbe) SetFollowerParkedCount(int, int) {
	p.record("follower_parked")
}
func (p *channelCompositionProbe) ObserveFollowerRecoveryProbe(string) {
	p.record("follower_probe")
}
func (p *channelCompositionProbe) ObservePull(string, bool) { p.record("pull") }
func (p *channelCompositionProbe) ObservePullHintResult(channeltransport.PullHintReason, string, error) {
	p.record("pull_hint_result")
}
func (p *channelCompositionProbe) ObservePullHintReceived(channeltransport.PullHintReason, string, error) {
	p.record("pull_hint_received")
}
func (p *channelCompositionProbe) SetPendingMetaCount(int, int) { p.record("pending_meta_count") }
func (p *channelCompositionProbe) ObservePendingMeta(string, error) {
	p.record("pending_meta")
}
func (p *channelCompositionProbe) ObserveNeedMetaPull(string, error) {
	p.record("need_meta_pull")
}
func (p *channelCompositionProbe) ObserveReplicationStage(string, string, time.Duration) {
	p.record("replication_stage")
}
func (p *channelCompositionProbe) ObserveChannelMetaCache(string) { p.record("meta_cache") }
func (p *channelCompositionProbe) ObserveAppendBatch(int, int, time.Duration) {
	p.record("append_batch")
}
func (p *channelCompositionProbe) ObserveAppendLatency(ch.CommitMode, time.Duration) {
	p.record("append_latency")
}
func (p *channelCompositionProbe) ObserveConversationHydrationBatch(string, int, int, int, time.Duration) {
	p.record("conversation_hydration")
}
func (p *channelCompositionProbe) ObserveAppendWaitStage(string, ch.CommitMode, string, time.Duration) {
	p.record("append_wait")
}
func (p *channelCompositionProbe) ObserveAppendWaitCanceled(reactor.AppendWaitCancelSnapshot) {
	p.record("append_cancel")
}
func (p *channelCompositionProbe) ObserveWorkerResult(worker.TaskKind, error, time.Duration) {
	p.record("worker_result")
}

type gatewayCompositionProbe struct {
	gatewayMetricsObserver
	calls int
}

func newGatewayCompositionProbe() *gatewayCompositionProbe { return &gatewayCompositionProbe{} }
func (p *gatewayCompositionProbe) mark()                   { p.calls++ }
func (p *gatewayCompositionProbe) OnConnectionOpen(gateway.ConnectionEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) OnConnectionClose(gateway.ConnectionEvent) { p.mark() }
func (p *gatewayCompositionProbe) OnAuth(gateway.AuthEvent)                  { p.mark() }
func (p *gatewayCompositionProbe) OnFrameIn(gateway.FrameEvent)              { p.mark() }
func (p *gatewayCompositionProbe) OnFrameOut(gateway.FrameEvent)             { p.mark() }
func (p *gatewayCompositionProbe) OnFrameHandled(gateway.FrameHandleEvent)   { p.mark() }
func (p *gatewayCompositionProbe) OnTransportWrite(gateway.TransportWriteEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) OnSessionError(gateway.SessionErrorEvent) { p.mark() }
func (p *gatewayCompositionProbe) OnAsyncSendQueue(gateway.AsyncSendQueueEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) OnAsyncSendAdmission(gateway.AsyncSendAdmissionEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) OnAsyncSendBatch(gateway.AsyncSendBatchEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) OnAsyncSendDispatchWait(gateway.AsyncSendDispatchWaitEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) OnAsyncAuthQueue(gateway.AsyncAuthQueueEvent) { p.mark() }
func (p *gatewayCompositionProbe) OnAsyncAuthAdmission(gateway.AsyncAuthAdmissionEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) OnAsyncAuthWait(gateway.AsyncAuthWaitEvent) { p.mark() }
func (p *gatewayCompositionProbe) OnTransportPressure(gateway.TransportPressureEvent) {
	p.mark()
}
func (p *gatewayCompositionProbe) SendackWritten(gatewayadapter.SendackEvent) { p.mark() }

type slotCompositionProbe struct {
	slotMetricsObserver
	calls int
}

func (p *slotCompositionProbe) SetSchedulerWorkers(int)                         { p.calls++ }
func (p *slotCompositionProbe) SetSchedulerInflight(int)                        { p.calls++ }
func (p *slotCompositionProbe) SetSchedulerState(multiraft.SchedulerStateEvent) { p.calls++ }
func (p *slotCompositionProbe) ObserveSchedulerAdmission(string)                { p.calls++ }
func (p *slotCompositionProbe) ObserveSchedulerTask(string, time.Duration)      { p.calls++ }
func (p *slotCompositionProbe) ObserveSlotProposal(multiraft.SlotID, time.Duration) {
	p.calls++
}
func (p *slotCompositionProbe) ObserveSlotProposalAdmission(multiraft.SlotID, multiraft.ProposalClass, string) {
	p.calls++
}
func (p *slotCompositionProbe) ObserveSlotLeaderChangeWithCause(multiraft.SlotID, multiraft.NodeID, multiraft.NodeID, multiraft.LeaderChangeCause) {
	p.calls++
}
func (p *slotCompositionProbe) SetSlotApplyState(multiraft.SlotID, uint64, uint64) {
	p.calls++
}

type transportCompositionProbe struct{ calls int }

func (p *transportCompositionProbe) ObserveTransport(transport.Event) { p.calls++ }

type controllerCompositionProbe struct {
	controllerRaftMetricsObserver
	calls int
}

func (p *controllerCompositionProbe) SetStepQueueDepth(int, int) { p.calls++ }
func (p *controllerCompositionProbe) ObserveStepEnqueue(string, time.Duration) {
	p.calls++
}
func (p *controllerCompositionProbe) SetApplyState(uint64, uint64) { p.calls++ }

type controlSnapshotCompositionProbe struct{ calls int }

func (p *controlSnapshotCompositionProbe) ObserveControlSnapshot(control.Snapshot) { p.calls++ }

type slotMoveCompositionProbe struct{ calls int }

func (p *slotMoveCompositionProbe) ObserveSlotReplicaMovePhase(string, string, time.Duration) {
	p.calls++
}

type preferredCompositionProbe struct{ calls int }

func (p *preferredCompositionProbe) ObservePreferredLeaderDecision(string) { p.calls++ }
func (p *preferredCompositionProbe) ObservePreferredLeaderStrictWait(string, time.Duration) {
	p.calls++
}
func (p *preferredCompositionProbe) ObservePreferredLeaderReconcile(clustertasks.PreferredLeaderObservation) {
	p.calls++
}

type commitCompositionProbe struct {
	storageCommitMetricsObserver
	calls int
}

func (p *commitCompositionProbe) SetCommitCoordinatorQueueDepth(int) { p.calls++ }
func (p *commitCompositionProbe) SetCommitCoordinatorQueue(int, int) { p.calls++ }
func (p *commitCompositionProbe) ObserveCommitCoordinatorBatch(messagedb.CommitCoordinatorBatchEvent) {
	p.calls++
}
func (p *commitCompositionProbe) ObserveCommitCoordinatorRequest(messagedb.CommitCoordinatorRequestEvent) {
	p.calls++
}

type messageEventCompositionProbe struct {
	messageEventMetricsObserver
	calls int
}

func (p *messageEventCompositionProbe) ObserveMessageEventAppend(cluster.MessageEventAppendObservation) {
	p.calls++
}
func (p *messageEventCompositionProbe) ObserveMessageEventAppendStage(cluster.MessageEventAppendStageObservation) {
	p.calls++
}
func (p *messageEventCompositionProbe) ObserveMessageEventPropose(cluster.MessageEventProposeObservation) {
	p.calls++
}
func (p *messageEventCompositionProbe) ObserveMessageEventProposeStage(cluster.MessageEventProposeStageObservation) {
	p.calls++
}
func (p *messageEventCompositionProbe) SetMessageEventStreamCache(cluster.MessageEventStreamCacheObservation) {
	p.calls++
}

type membershipCompositionProbe struct{ calls int }

func (p *membershipCompositionProbe) ObserveMembershipMutation(cluster.MembershipMutationObservation) {
	p.calls++
}

var (
	_ reactor.Observer                              = (*channelCompositionProbe)(nil)
	_ clusterchannels.ConversationHydrationObserver = (*channelCompositionProbe)(nil)
	_ controller.RaftObserver                       = (*controllerCompositionProbe)(nil)
)
