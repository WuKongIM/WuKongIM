package cluster

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
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
		{name: "created", applyData: metafsm.EncodeCreateChannelRuntimeMetaResult(metafsm.CreateChannelRuntimeMetaResult{Created: true}), wantResult: clusterchannels.MetaCreateCreated},
		{name: "already existing", applyData: metafsm.EncodeCreateChannelRuntimeMetaResult(metafsm.CreateChannelRuntimeMetaResult{}), wantResult: clusterchannels.MetaCreateAlreadyExisting},
		{name: "wait error", waitErr: errors.New("apply failed"), wantResult: clusterchannels.MetaCreateError},
		{name: "decode error", applyData: []byte("invalid"), wantResult: clusterchannels.MetaCreateError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: tt.applyData, err: tt.waitErr}}
			observer := &recordingMetaCreateObserver{}
			proposer := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
			command := metafsm.EncodeCreateChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
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

func TestDefaultSlotProposerDoesNotObserveRejectedMetaCreateSubmission(t *testing.T) {
	runtime := &recordingSlotRuntime{err: multiraft.ErrNotLeader}
	observer := &recordingMetaCreateObserver{}
	proposer := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
	command := metafsm.EncodeCreateChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
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

func TestDefaultSlotProposerForwardHandlerObservesMetaCreateOnlyOnLeader(t *testing.T) {
	tests := []struct {
		name       string
		applyData  []byte
		waitErr    error
		wantResult clusterchannels.MetaCreateResult
		wantErr    bool
	}{
		{name: "created", applyData: metafsm.EncodeCreateChannelRuntimeMetaResult(metafsm.CreateChannelRuntimeMetaResult{Created: true}), wantResult: clusterchannels.MetaCreateCreated},
		{name: "already existing", applyData: metafsm.EncodeCreateChannelRuntimeMetaResult(metafsm.CreateChannelRuntimeMetaResult{}), wantResult: clusterchannels.MetaCreateAlreadyExisting},
		{name: "future failure", waitErr: errors.New("apply failed"), wantResult: clusterchannels.MetaCreateError, wantErr: true},
		{name: "decode failure", applyData: []byte("invalid"), wantResult: clusterchannels.MetaCreateError, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: tt.applyData, err: tt.waitErr}}
			observer := &recordingMetaCreateObserver{}
			leader := defaultSlotProposer{runtime: runtime, metaCreateObserver: observer}
			handler := propose.NewForwardHandler(leader)
			command := metafsm.EncodeCreateChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
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
	command := metafsm.EncodeCreateChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
		ChannelID: "leader-change", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
	})
	runtime := &retryingSlotRuntime{
		future: recordingSlotFuture{data: metafsm.EncodeCreateChannelRuntimeMetaResult(metafsm.CreateChannelRuntimeMetaResult{Created: true})},
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
	command := metafsm.EncodeCreateChannelRuntimeMetaCommand(metadb.ChannelRuntimeMeta{
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

	runtime := &recordingSlotRuntime{future: recordingSlotFuture{data: metafsm.EncodeCreateChannelRuntimeMetaResult(metafsm.CreateChannelRuntimeMetaResult{Created: true})}}
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

func TestDefaultChannelRuntimeMetaStoreCreatesWithAuthoritativeResult(t *testing.T) {
	proposer := &recordingNodeResultProposer{
		result: metafsm.EncodeCreateChannelRuntimeMetaResult(metafsm.CreateChannelRuntimeMetaResult{}),
	}
	node := &Node{proposer: proposer}
	node.started.Store(true)
	store := defaultChannelRuntimeMetaStore{node: node}
	meta := metadb.ChannelRuntimeMeta{
		ChannelID: "create-result", ChannelType: 1, ChannelEpoch: 1, LeaderEpoch: 1,
		Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1,
	}

	result, err := store.CreateChannelRuntimeMeta(context.Background(), meta)
	if err != nil {
		t.Fatalf("CreateChannelRuntimeMeta() error = %v", err)
	}
	if result.Created {
		t.Fatal("CreateChannelRuntimeMeta() Created = true, want authoritative already-existing result")
	}
	if proposer.resultCalls != 1 || proposer.proposeCalls != 0 {
		t.Fatalf("resultCalls=%d proposeCalls=%d, want result proposal only", proposer.resultCalls, proposer.proposeCalls)
	}
	if !metafsm.IsCreateChannelRuntimeMetaCommand(proposer.last.Command) {
		t.Fatalf("command = %x, want create-only runtime metadata command", proposer.last.Command)
	}
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
