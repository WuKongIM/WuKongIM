package cluster

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

func TestDefaultSlotProposerObservesMetaCreateSubmitAndWait(t *testing.T) {
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{}}
	observer := &recordingAppendStageObserver{}
	ctx := propose.WithStageObserver(context.Background(), observer)

	err := defaultSlotProposer{runtime: runtime}.Propose(ctx, 7, propose.EncodePayload(11, []byte("cmd")))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if runtime.proposeCalls != 1 || runtime.slotID != 7 {
		t.Fatalf("runtime propose = %d slot=%d, want one call to slot 7", runtime.proposeCalls, runtime.slotID)
	}
	if len(runtime.payload) != slotProposalEnvelopeSize+len("cmd") {
		t.Fatalf("runtime payload len = %d, want %d", len(runtime.payload), slotProposalEnvelopeSize+len("cmd"))
	}
	if hashSlot := binary.BigEndian.Uint16(runtime.payload[:2]); hashSlot != 11 {
		t.Fatalf("runtime payload hashSlot = %d, want 11", hashSlot)
	}
	if createdAtMS := binary.BigEndian.Uint64(runtime.payload[2:slotProposalEnvelopeSize]); createdAtMS == 0 {
		t.Fatalf("runtime payload created_at_ms = 0, want non-zero")
	}
	if command := string(runtime.payload[slotProposalEnvelopeSize:]); command != "cmd" {
		t.Fatalf("runtime payload command = %q, want cmd", command)
	}
	multiraft.ObserveProposalStage(runtime.ctx, "meta_create_slot_raft_commit_wait", nil, time.Millisecond)
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_propose_submit", "ok")
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_propose_wait", "ok")
	requireRecordedAppendStage(t, observer.events, "meta_create_slot_raft_commit_wait", "ok")
}

func TestDefaultSlotProposerProposeResultReturnsApplyData(t *testing.T) {
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: []byte("apply-result")}}

	got, err := defaultSlotProposer{runtime: runtime}.ProposeResult(context.Background(), 7, propose.EncodePayload(11, []byte("cmd")))
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if string(got) != "apply-result" {
		t.Fatalf("result = %q, want apply-result", got)
	}
	if runtime.proposeCalls != 1 || runtime.slotID != 7 {
		t.Fatalf("runtime propose = %d slot=%d, want one call to slot 7", runtime.proposeCalls, runtime.slotID)
	}
}

func TestDefaultSlotProposerObservesAuthoritativeMetaCreateResultOnce(t *testing.T) {
	tests := []struct {
		name       string
		applyData  []byte
		waitErr    error
		wantResult clusterchannels.MetaCreateResult
	}{
		{name: "created", applyData: encodeTestRuntimeMetaCreateResult(11, "authoritative", 1, true), wantResult: clusterchannels.MetaCreateCreated},
		{name: "already existing", applyData: encodeTestRuntimeMetaCreateResult(11, "authoritative", 1, false), wantResult: clusterchannels.MetaCreateAlreadyExisting},
		{name: "wait error", waitErr: errors.New("apply failed"), wantResult: clusterchannels.MetaCreateError},
		{name: "decode error", applyData: []byte("invalid"), wantResult: clusterchannels.MetaCreateError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: tt.applyData, err: tt.waitErr}}
			observer := &recordingMetaCreateObserver{}
			proposer := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
			command := mustEncodeTestRuntimeMetaCreateCommand(t, 11, metadb.ChannelRuntimeMeta{
				ChannelID: "authoritative", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
				Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
			})

			_, _ = proposer.ProposeResult(context.Background(), 37, propose.EncodePayload(11, command))

			if len(observer.events) != 1 {
				t.Fatalf("events = %#v, want exactly one authoritative observation", observer.events)
			}
			if got := observer.events[0]; got.slotID != 37 || got.result != tt.wantResult {
				t.Fatalf("event = %#v, want slot 37 result %q", got, tt.wantResult)
			}
		})
	}
}

func TestDefaultSlotProposerObservesRuntimeMetaInsidePersonDirectoryPrepare(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: "person-prepare", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
	}
	command, err := metafsm.EncodePreparePersonChannelDirectoryBatchCommandChecked(nil, []metafsm.CreateChannelRuntimeMetaBatchItem{{
		HashSlot: 11, Meta: meta,
	}})
	if err != nil {
		t.Fatalf("EncodePreparePersonChannelDirectoryBatchCommandChecked() error = %v", err)
	}
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: encodeTestRuntimeMetaCreateResult(11, meta.ChannelID, meta.ChannelType, true)}}
	observer := &recordingMetaCreateObserver{}
	if err := (defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}).Propose(
		context.Background(), 37, propose.EncodePayload(11, command),
	); err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if len(observer.events) != 1 || observer.events[0].slotID != 37 || observer.events[0].result != clusterchannels.MetaCreateCreated {
		t.Fatalf("meta-create observations = %#v", observer.events)
	}
}

func TestDefaultSlotProposerMetaCreateObserverUsesRouteSlotRaftGroupNotPayloadHashSlot(t *testing.T) {
	const (
		routeSlotID     = uint32(3)
		payloadHashSlot = uint16(37)
	)
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: encodeTestRuntimeMetaCreateResult(
		payloadHashSlot, "slot-contract", 1, true,
	)}}
	observer := &recordingMetaCreateObserver{}
	command := mustEncodeTestRuntimeMetaCreateCommand(t, payloadHashSlot, metadb.ChannelRuntimeMeta{
		ChannelID: "slot-contract", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
	})

	_, err := (defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}).ProposeResult(
		context.Background(), routeSlotID, propose.EncodePayload(payloadHashSlot, command),
	)
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if runtime.slotID != multiraft.SlotID(routeSlotID) || binary.BigEndian.Uint16(runtime.payload[:2]) != payloadHashSlot {
		t.Fatalf("runtime slot=%d payload hash slot=%d, want route Slot Raft Group %d and hash slot %d",
			runtime.slotID, binary.BigEndian.Uint16(runtime.payload[:2]), routeSlotID, payloadHashSlot)
	}
	if len(observer.events) != 1 || observer.events[0].slotID != routeSlotID || observer.events[0].result != clusterchannels.MetaCreateCreated {
		t.Fatalf("events = %#v, want one created observation for route Slot Raft Group %d", observer.events, routeSlotID)
	}
}

func TestDefaultSlotProposerDoesNotObserveRejectedMetaCreateSubmission(t *testing.T) {
	runtime := &recordingSlotRuntime{err: multiraft.ErrNotLeader}
	observer := &recordingMetaCreateObserver{}
	proposer := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
	command := mustEncodeTestRuntimeMetaCreateCommand(t, 11, metadb.ChannelRuntimeMeta{
		ChannelID: "rejected", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
	})

	_, err := proposer.ProposeResult(context.Background(), 37, propose.EncodePayload(11, command))
	if !errors.Is(err, propose.ErrNotLeader) {
		t.Fatalf("ProposeResult() error = %v, want ErrNotLeader", err)
	}
	if len(observer.events) != 0 {
		t.Fatalf("events = %#v, want no event before an authoritative future exists", observer.events)
	}
}

func TestDefaultSlotProposerObservesMetaCreateOnlyAfterCanceledCallerFutureResolves(t *testing.T) {
	tests := []struct {
		name       string
		result     multiraft.Result
		resolveErr error
		wantResult clusterchannels.MetaCreateResult
	}{
		{
			name:       "created",
			result:     multiraft.Result{Data: encodeTestRuntimeMetaCreateResult(11, "canceled", 1, true)},
			wantResult: clusterchannels.MetaCreateCreated,
		},
		{
			name:       "already existing",
			result:     multiraft.Result{Data: encodeTestRuntimeMetaCreateResult(11, "canceled", 1, false)},
			wantResult: clusterchannels.MetaCreateAlreadyExisting,
		},
		{
			name:       "authoritative failure",
			resolveErr: errors.New("apply failed"),
			wantResult: clusterchannels.MetaCreateError,
		},
		{
			name:       "decode failure",
			result:     multiraft.Result{Data: []byte("invalid")},
			wantResult: clusterchannels.MetaCreateError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			future := newPendingSlotFuture()
			runtime := &recordingSlotRuntime{future: future}
			observer := &recordingMetaCreateObserver{}
			proposer := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
			command := mustEncodeTestRuntimeMetaCreateCommand(t, 11, metadb.ChannelRuntimeMeta{
				ChannelID: "canceled", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
				Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
			})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				_, err := proposer.ProposeResult(ctx, 37, propose.EncodePayload(11, command))
				done <- err
			}()

			<-future.waitStarted
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("ProposeResult() error = %v, want context canceled", err)
			}
			if len(observer.events) != 0 {
				t.Fatalf("events at caller cancellation = %#v, want none before future resolution", observer.events)
			}

			future.resolve(tt.result, tt.resolveErr)
			if len(observer.events) != 1 {
				t.Fatalf("events after future resolution = %#v, want exactly one", observer.events)
			}
			if got := observer.events[0]; got.slotID != 37 || got.result != tt.wantResult {
				t.Fatalf("event = %#v, want slot 37 result %q", got, tt.wantResult)
			}
		})
	}
}

func TestDefaultSlotProposerForwardHandlerObservesMetaCreateOnlyOnLeader(t *testing.T) {
	tests := []struct {
		name       string
		applyData  []byte
		waitErr    error
		wantResult clusterchannels.MetaCreateResult
		wantErr    bool
	}{
		{name: "created", applyData: encodeTestRuntimeMetaCreateResult(19, "forwarded", 1, true), wantResult: clusterchannels.MetaCreateCreated},
		{name: "already existing", applyData: encodeTestRuntimeMetaCreateResult(19, "forwarded", 1, false), wantResult: clusterchannels.MetaCreateAlreadyExisting},
		{name: "future failure", waitErr: errors.New("apply failed"), wantResult: clusterchannels.MetaCreateError, wantErr: true},
		{name: "decode failure", applyData: []byte("invalid"), wantResult: clusterchannels.MetaCreateError, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: tt.applyData, err: tt.waitErr}}
			observer := &recordingMetaCreateObserver{}
			leader := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
			handler := propose.NewForwardHandler(leader)
			command := mustEncodeTestRuntimeMetaCreateCommand(t, 19, metadb.ChannelRuntimeMeta{
				ChannelID: "forwarded", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
				Leader: 2, Replicas: []uint64{2}, ISR: []uint64{2}, MinISR: 1,
			})
			payload, err := propose.EncodeForwardRequest(propose.ForwardRequest{
				SlotID: 41, HashSlot: 19, WantResult: true,
				Payload: propose.EncodePayload(19, command),
			})
			if err != nil {
				t.Fatalf("EncodeForwardRequest() error = %v", err)
			}

			_, err = handler.HandleRPC(context.Background(), payload)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HandleRPC() error = %v, wantErr=%t", err, tt.wantErr)
			}
			if len(observer.events) != 1 {
				t.Fatalf("events = %#v, want exactly one leader-side forwarded observation", observer.events)
			}
			if got := observer.events[0]; got.slotID != 41 || got.result != tt.wantResult {
				t.Fatalf("event = %#v, want slot 41 result %q", got, tt.wantResult)
			}
		})
	}
}

func TestProposeServiceLeaderChangeRetryObservesMetaCreateOnce(t *testing.T) {
	command := mustEncodeTestRuntimeMetaCreateCommand(t, 19, metadb.ChannelRuntimeMeta{
		ChannelID: "leader-change", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
	})
	runtime := &retryingSlotRuntime{
		future: recordingSlotFuture{data: encodeTestRuntimeMetaCreateResult(19, "leader-change", 1, true)},
	}
	observer := &recordingMetaCreateObserver{}
	slots := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
	router := &countingProposeRouter{route: routing.Route{HashSlot: 19, SlotID: 41, Leader: 1}}
	service := propose.NewService(propose.Config{LocalNode: 1, Router: router, Slots: slots})

	_, err := service.ProposeResult(context.Background(), propose.Request{Key: "leader-change", Command: command})
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if runtime.calls != 2 || router.calls != 2 {
		t.Fatalf("runtime calls=%d route calls=%d, want rejected attempt plus successful retry", runtime.calls, router.calls)
	}
	if len(observer.events) != 1 {
		t.Fatalf("events = %#v, want only the eventual authoritative result", observer.events)
	}
	if got := observer.events[0]; got.slotID != 41 || got.result != clusterchannels.MetaCreateCreated {
		t.Fatalf("event = %#v, want slot 41 created", got)
	}
}

func TestDefaultSlotProposerMetaCreateObservationIsNotReplicaApplied(t *testing.T) {
	observer := &recordingMetaCreateObserver{}
	command := mustEncodeTestRuntimeMetaCreateCommand(t, 11, metadb.ChannelRuntimeMeta{
		ChannelID: "three-replicas", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2,
	})

	for replica := 1; replica <= 3; replica++ {
		db, err := metadb.Open(t.TempDir())
		if err != nil {
			t.Fatalf("Open(replica %d) error = %v", replica, err)
		}
		sm, err := metafsm.NewStateMachineWithHashSlots(db, 37, []uint16{11})
		if err != nil {
			_ = db.Close()
			t.Fatalf("NewStateMachineWithHashSlots(replica %d) error = %v", replica, err)
		}
		_, err = sm.Apply(context.Background(), multiraft.Command{
			SlotID: 37, HashSlot: 11, Index: 1, Term: 1, Data: command,
		})
		closeErr := db.Close()
		if err != nil {
			t.Fatalf("Apply(replica %d) error = %v", replica, err)
		}
		if closeErr != nil {
			t.Fatalf("Close(replica %d) error = %v", replica, closeErr)
		}
	}
	if len(observer.events) != 0 {
		t.Fatalf("replica apply events = %#v, want none", observer.events)
	}

	runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: encodeTestRuntimeMetaCreateResult(11, "three-replicas", 1, true)}}
	proposer := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
	_, err := proposer.ProposeResult(context.Background(), 37, propose.EncodePayload(11, command))
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if len(observer.events) != 1 || observer.events[0].result != clusterchannels.MetaCreateCreated {
		t.Fatalf("authoritative events = %#v, want one created observation", observer.events)
	}
}

func TestDefaultSlotProposerDoesNotObserveNonCreateProposal(t *testing.T) {
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{}}
	observer := &recordingMetaCreateObserver{}
	proposer := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}

	_, err := proposer.ProposeResult(context.Background(), 7, propose.EncodePayload(11, metafsm.EncodeUpsertChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{ChannelID: "repair", ChannelType: 1})))
	if err != nil {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if len(observer.events) != 0 {
		t.Fatalf("events = %#v, want no observation for ordinary upsert", observer.events)
	}
}

func TestDefaultChannelRuntimeMetaStoreCreatesBatchWithAuthoritativeResults(t *testing.T) {
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: "create-result", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
	}
	router := routing.NewRouter()
	if err := router.UpdateControlSnapshot(routeAuthoritySnapshot(1)); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 1}})
	route, err := router.RouteKey(meta.ChannelID)
	if err != nil {
		t.Fatalf("RouteKey() error = %v", err)
	}
	proposer := &recordingNodeResultProposer{
		result: encodeTestRuntimeMetaCreateResult(route.HashSlot, meta.ChannelID, meta.ChannelType, false),
	}
	node := &Node{proposer: proposer, router: router}
	node.started.Store(true)
	store := defaultChannelRuntimeMetaStore{node: node}

	results, err := store.CreateChannelRuntimeMetaBatch(context.Background(), route, []clusterchannels.RuntimeMetaCreateItem{{
		HashSlot: route.HashSlot,
		Meta:     meta,
	}})
	if err != nil {
		t.Fatalf("CreateChannelRuntimeMetaBatch() error = %v", err)
	}
	if len(results) != 1 || results[0].Created {
		t.Fatalf("CreateChannelRuntimeMetaBatch() results = %#v, want authoritative already-existing result", results)
	}
	if proposer.resultCalls != 1 || proposer.proposeCalls != 0 {
		t.Fatalf("resultCalls=%d proposeCalls=%d, want result proposal only", proposer.resultCalls, proposer.proposeCalls)
	}
	if !metafsm.IsCreateChannelRuntimeMetaCommand(proposer.last.Command) {
		t.Fatalf("command = %x, want create-only runtime metadata command", proposer.last.Command)
	}
	if proposer.last.Target.HashSlot != route.HashSlot || proposer.last.Target.SlotID != route.SlotID ||
		!proposer.last.Target.HasHashSlot || !proposer.last.Target.HasSlotID {
		t.Fatalf("proposal target = %#v, want exact route %#v", proposer.last.Target, route)
	}
}

func TestDefaultChannelRuntimeMetaStoreAuthoritativeRereadFollowsCurrentLeader(t *testing.T) {
	meta := metadb.NormalizeChannelRuntimeMeta(metadb.ChannelRuntimeMeta{
		ChannelID: "reread-current-leader", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2,
	})
	router := routing.NewRouter()
	if err := router.UpdateControlSnapshot(routeAuthoritySnapshot(1)); err != nil {
		t.Fatalf("UpdateControlSnapshot() error = %v", err)
	}
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 1}})
	expected, err := router.RouteKey(meta.ChannelID)
	if err != nil {
		t.Fatalf("RouteKey(initial) error = %v", err)
	}

	db, err := metadb.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.ForHashSlot(expected.HashSlot).UpsertChannelRuntimeMeta(context.Background(), meta); err != nil {
		t.Fatalf("UpsertChannelRuntimeMeta() error = %v", err)
	}

	node := &Node{cfg: Config{NodeID: 2}, router: router}
	node.started.Store(true)
	node.defaultSlotProxy = slotproxy.NewChannelMetadataStore(node, db)
	router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: expected.SlotID, Leader: 2, LeaderTerm: expected.LeaderTerm + 1}})

	store := defaultChannelRuntimeMetaStore{node: node}
	reads, err := store.BatchGetChannelRuntimeMetas(context.Background(), expected, []clusterchannels.RuntimeMetaCreateItem{{
		HashSlot: expected.HashSlot,
		Meta:     meta,
	}})
	if err != nil {
		t.Fatalf("BatchGetChannelRuntimeMetas() error = %v, want current-leader reread", err)
	}
	if len(reads) != 1 || reads[0].Err != nil || reads[0].Meta.ChannelID != meta.ChannelID {
		t.Fatalf("BatchGetChannelRuntimeMetas() = %#v, want authoritative current-leader row", reads)
	}
}

func mustEncodeTestRuntimeMetaCreateCommand(t *testing.T, hashSlot uint16, meta metadb.ChannelRuntimeMeta) []byte {
	t.Helper()
	command, err := metafsm.EncodeCreateChannelRuntimeMetaBatchCommandChecked([]metafsm.CreateChannelRuntimeMetaBatchItem{{
		HashSlot: hashSlot,
		Meta:     meta,
	}})
	if err != nil {
		t.Fatalf("EncodeCreateChannelRuntimeMetaBatchCommandChecked() error = %v", err)
	}
	return command
}

func encodeTestRuntimeMetaCreateResult(hashSlot uint16, channelID string, channelType int64, created bool) []byte {
	return metafsm.EncodeCreateChannelRuntimeMetaBatchResult([]metafsm.CreateChannelRuntimeMetaBatchResult{{
		HashSlot: hashSlot, ChannelID: channelID, ChannelType: channelType, Created: created,
	}})
}

func TestDefaultSlotProposerPassesBackgroundProposalClass(t *testing.T) {
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{}}
	ctx := propose.WithProposalClass(context.Background(), propose.ProposalClassBackground)

	err := defaultSlotProposer{runtime: runtime}.Propose(ctx, 7, propose.EncodePayload(11, []byte("cmd")))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if got := multiraft.ProposalClassFromContext(runtime.ctx); got != multiraft.ProposalClassBackground {
		t.Fatalf("runtime proposal class = %q, want %q", got, multiraft.ProposalClassBackground)
	}
}

func TestDefaultSlotProposerRejectsDuringMaintenance(t *testing.T) {
	runtime := &recordingSlotRuntime{future: recordingSlotFuture{}}
	proposer := defaultSlotProposer{
		runtime: runtime,
		acquireAdmission: func() (func(), error) {
			return nil, ErrMaintenance
		},
	}

	err := proposer.Propose(context.Background(), 7, propose.EncodePayload(11, []byte("cmd")))

	if !errors.Is(err, ErrMaintenance) {
		t.Fatalf("Propose() error = %v, want ErrMaintenance", err)
	}
	if runtime.proposeCalls != 0 {
		t.Fatalf("runtime propose calls = %d, want 0 during maintenance", runtime.proposeCalls)
	}
}

type recordingSlotRuntime struct {
	proposeCalls int
	slotID       multiraft.SlotID
	payload      []byte
	ctx          context.Context
	future       multiraft.Future
	err          error
}

func (r *recordingSlotRuntime) Propose(ctx context.Context, slotID multiraft.SlotID, payload []byte) (multiraft.Future, error) {
	r.proposeCalls++
	r.ctx = ctx
	r.slotID = slotID
	r.payload = append([]byte(nil), payload...)
	return r.future, r.err
}

func (r *recordingSlotRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	return multiraft.Status{Role: multiraft.RoleLeader}, nil
}

type recordingSlotFuture struct {
	data []byte
	err  error
}

type pendingSlotFuture struct {
	waitStarted chan struct{}
	done        chan struct{}
	mu          sync.Mutex
	result      multiraft.Result
	err         error
	observer    multiraft.FutureCompletionObserver
	resolved    bool
}

func newPendingSlotFuture() *pendingSlotFuture {
	return &pendingSlotFuture{waitStarted: make(chan struct{}), done: make(chan struct{})}
}

func (f *pendingSlotFuture) Wait(ctx context.Context) (multiraft.Result, error) {
	select {
	case <-f.waitStarted:
	default:
		close(f.waitStarted)
	}
	select {
	case <-ctx.Done():
		return multiraft.Result{}, ctx.Err()
	case <-f.done:
		return f.result, f.err
	}
}

func (f *pendingSlotFuture) resolve(result multiraft.Result, err error) {
	f.mu.Lock()
	f.result = result
	f.err = err
	f.resolved = true
	observer := f.observer
	f.mu.Unlock()
	if observer != nil {
		observer.ObserveFutureCompletion(result, err)
	}
	close(f.done)
}

func (f *pendingSlotFuture) ObserveCompletion(observer multiraft.FutureCompletionObserver) bool {
	f.mu.Lock()
	if f.observer != nil {
		f.mu.Unlock()
		return false
	}
	f.observer = observer
	resolved := f.resolved
	result, err := f.result, f.err
	f.mu.Unlock()
	if resolved {
		observer.ObserveFutureCompletion(result, err)
	}
	return true
}

type retryingSlotRuntime struct {
	calls  int
	future multiraft.Future
}

func (r *retryingSlotRuntime) Propose(context.Context, multiraft.SlotID, []byte) (multiraft.Future, error) {
	r.calls++
	if r.calls == 1 {
		return nil, multiraft.ErrNotLeader
	}
	return r.future, nil
}

func (r *retryingSlotRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	return multiraft.Status{Role: multiraft.RoleLeader}, nil
}

type countingProposeRouter struct {
	route routing.Route
	calls int
}

func (r *countingProposeRouter) RouteKey(string) (routing.Route, error) {
	r.calls++
	return r.route, nil
}

func (r *countingProposeRouter) RouteHashSlot(uint16) (routing.Route, error) {
	r.calls++
	return r.route, nil
}

func (r *countingProposeRouter) RouteSlot(uint32, uint16) (routing.Route, error) {
	r.calls++
	return r.route, nil
}

func (f recordingSlotFuture) Wait(context.Context) (multiraft.Result, error) {
	return multiraft.Result{Data: append([]byte(nil), f.data...)}, f.err
}

func (f recordingSlotFuture) ObserveCompletion(observer multiraft.FutureCompletionObserver) bool {
	if observer == nil {
		return false
	}
	observer.ObserveFutureCompletion(multiraft.Result{Data: append([]byte(nil), f.data...)}, f.err)
	return true
}

type recordingAppendStageObserver struct {
	events []recordedAppendStage
}

func (o *recordingAppendStageObserver) ObserveChannelAppendStage(stage string, result string, _ time.Duration) {
	o.events = append(o.events, recordedAppendStage{stage: stage, result: result})
}

type recordedAppendStage struct {
	stage  string
	result string
}

type recordingMetaCreateObserver struct {
	events []recordedMetaCreate
}

func (o *recordingMetaCreateObserver) ObserveChannelMetaCreate(slotID uint32, result clusterchannels.MetaCreateResult) {
	o.events = append(o.events, recordedMetaCreate{slotID: slotID, result: result})
}

type recordedMetaCreate struct {
	slotID uint32
	result clusterchannels.MetaCreateResult
}

type recordingNodeResultProposer struct {
	result       []byte
	last         propose.Request
	proposeCalls int
	resultCalls  int
}

func (p *recordingNodeResultProposer) Propose(context.Context, propose.Request) error {
	p.proposeCalls++
	return nil
}

func (p *recordingNodeResultProposer) ProposeResult(_ context.Context, req propose.Request) ([]byte, error) {
	p.resultCalls++
	p.last = req
	return append([]byte(nil), p.result...), nil
}

func requireRecordedAppendStage(t *testing.T, events []recordedAppendStage, stage string, result string) {
	t.Helper()
	for _, event := range events {
		if event.stage == stage && event.result == result {
			return
		}
	}
	t.Fatalf("append stage %s/%s not observed in %#v", stage, result, events)
}
