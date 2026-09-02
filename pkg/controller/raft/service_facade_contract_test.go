package raft

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"go.etcd.io/raft/v3/raftpb"
)

type serviceObserverCapture struct {
	depth, capacity int
	result          string
	duration        time.Duration
	commit, applied uint64
}

func (o *serviceObserverCapture) SetStepQueueDepth(depth, capacity int) {
	o.depth, o.capacity = depth, capacity
}
func (o *serviceObserverCapture) ObserveStepEnqueue(result string, duration time.Duration) {
	o.result, o.duration = result, duration
}
func (o *serviceObserverCapture) SetApplyState(commit, applied uint64) {
	o.commit, o.applied = commit, applied
}

func TestServiceFacadeReportsUnavailableLifecycleWithoutStartingRaft(t *testing.T) {
	service := &Service{cfg: Config{NodeID: 7}, status: Status{NodeID: 7}}
	cmd := command.Command{Kind: command.KindUpsertNode}
	if err := service.Propose(nil, cmd); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := service.ProposeResult(context.Background(), cmd); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ProposeResult() error = %v", err)
	}
	if err := service.ProbePropose(context.Background()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ProbePropose() error = %v", err)
	}
	if _, err := service.AddLearner(context.Background(), 8); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("AddLearner() error = %v", err)
	}
	if _, err := service.PromoteLearner(context.Background(), 8); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("PromoteLearner() error = %v", err)
	}
	if _, err := service.AddLearner(context.Background(), 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("AddLearner(0) error = %v", err)
	}
	if err := service.Step(context.Background(), raftpb.Message{To: 8}); err != nil {
		t.Fatalf("Step(other node): %v", err)
	}
	if err := service.Step(context.Background(), raftpb.Message{To: 7}); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Step(local) error = %v", err)
	}
	service.stopped = true
	if err := service.Propose(context.Background(), cmd); !errors.Is(err, ErrStopped) {
		t.Fatalf("Propose(stopped) error = %v", err)
	}
	if got := (*Service)(nil).LeaderID(); got != 0 {
		t.Fatalf("nil LeaderID() = %d", got)
	}
	service.leaderID.Store(9)
	if got := service.LeaderID(); got != 9 {
		t.Fatalf("LeaderID() = %d, want 9", got)
	}
	rejected := ProposalRejectedError{Index: 12, Reason: "revision mismatch"}
	if !errors.Is(rejected, ErrProposalRejected) || rejected.Error() != "controller/raft: proposal rejected at index 12: revision mismatch" {
		t.Fatalf("ProposalRejectedError = %v", rejected)
	}
}

func TestServiceFacadeRoutesRequestsAndStepWithOwnedResponses(t *testing.T) {
	observer := &serviceObserverCapture{}
	service := &Service{
		cfg: Config{NodeID: 7, Observer: observer}, started: true,
		proposal: make(chan proposalRequest), stepCh: make(chan raftpb.Message, 2),
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	handle := func(response proposalResponse) <-chan proposalRequest {
		captured := make(chan proposalRequest, 1)
		go func() {
			req := <-service.proposal
			captured <- req
			req.resp <- response
		}()
		return captured
	}

	wantResult := ProposalResult{Changed: true, Revision: 4, AppliedRaftIndex: 9}
	captured := handle(proposalResponse{result: wantResult})
	gotResult, err := service.ProposeResult(context.Background(), command.Command{Kind: command.KindUpsertNode})
	if err != nil || gotResult != wantResult {
		t.Fatalf("ProposeResult() = (%+v, %v)", gotResult, err)
	}
	if req := <-captured; req.probe || req.confChange != nil || req.cmd.Kind != command.KindUpsertNode {
		t.Fatalf("proposal request = %+v", req)
	}

	captured = handle(proposalResponse{})
	if err := service.Propose(context.Background(), command.Command{Kind: command.KindCompleteTask}); err != nil {
		t.Fatalf("Propose(): %v", err)
	}
	<-captured

	wantMembership := MembershipChangeResult{Index: 11, ConfState: raftpb.ConfState{Learners: []uint64{8}}}
	captured = handle(proposalResponse{membership: wantMembership})
	gotMembership, err := service.AddLearner(context.Background(), 8)
	if err != nil || gotMembership.Index != 11 || len(gotMembership.ConfState.Learners) != 1 {
		t.Fatalf("AddLearner() = (%+v, %v)", gotMembership, err)
	}
	if req := <-captured; req.confChange == nil || req.confChange.Type != raftpb.ConfChangeAddLearnerNode || req.confChange.NodeID != 8 {
		t.Fatalf("learner request = %+v", req)
	}

	wantMembership = MembershipChangeResult{Index: 12, ConfState: raftpb.ConfState{Voters: []uint64{8}}}
	captured = handle(proposalResponse{membership: wantMembership})
	gotMembership, err = service.PromoteLearner(context.Background(), 8)
	if err != nil || gotMembership.Index != 12 || len(gotMembership.ConfState.Voters) != 1 {
		t.Fatalf("PromoteLearner() = (%+v, %v)", gotMembership, err)
	}
	if req := <-captured; req.confChange == nil || req.confChange.Type != raftpb.ConfChangeAddNode || req.confChange.NodeID != 8 {
		t.Fatalf("promotion request = %+v", req)
	}

	message := raftpb.Message{From: 8, To: 7, Type: raftpb.MsgHeartbeat}
	if err := service.Step(context.Background(), message); err != nil {
		t.Fatalf("Step(): %v", err)
	}
	if got := <-service.stepCh; got.From != 8 || got.To != 7 || got.Type != raftpb.MsgHeartbeat {
		t.Fatalf("queued step = %+v", got)
	}
	if observer.depth != 1 || observer.capacity != 2 || observer.result != "ok" || observer.duration < 0 {
		t.Fatalf("step observation = %+v", observer)
	}
	service.observeStepEnqueue("clamped", -time.Second)
	if observer.result != "clamped" || observer.duration != 0 {
		t.Fatalf("negative duration observation = %+v", observer)
	}
	service.observeApplyState(20, 19)
	if observer.commit != 20 || observer.applied != 19 {
		t.Fatalf("apply observation = %+v", observer)
	}
}

func TestServiceRunErrorAndTaskObserverPreserveFailureEvidence(t *testing.T) {
	service := &Service{cfg: Config{NodeID: 3}, started: true}
	if err := service.currentError(); !errors.Is(err, ErrStopped) {
		t.Fatalf("initial currentError() = %v", err)
	}
	runErr := errors.New("WAL failed")
	service.setRunError(runErr)
	if service.started || !errors.Is(service.currentError(), runErr) {
		t.Fatalf("run error state: started=%v err=%v", service.started, service.currentError())
	}
	status := service.Status()
	if !status.Degraded || status.NodeID != 3 || status.Role != RoleUnknown || status.ErrorReason != runErr.Error() {
		t.Fatalf("degraded status = %+v", status)
	}

	var got []fsm.TaskTransition
	observer := TaskTransitionObserverFunc(func(items []fsm.TaskTransition) { got = append(got, items...) })
	observer.ObserveControllerTaskTransitions([]fsm.TaskTransition{{AppliedRaftIndex: 17}})
	if len(got) != 1 || got[0].AppliedRaftIndex != 17 {
		t.Fatalf("task transitions = %+v", got)
	}
	TaskTransitionObserverFunc(nil).ObserveControllerTaskTransitions(nil)
}

func TestCompactLogRoutesLifecycleCancellationAndOwnedResponse(t *testing.T) {
	unstarted := &Service{cfg: Config{NodeID: 7}}
	result, err := unstarted.CompactLog(nil)
	if !errors.Is(err, ErrNotStarted) || result.NodeID != 7 || result.SkippedReason != LogCompactionSkipNotStarted {
		t.Fatalf("unstarted CompactLog() = (%+v, %v)", result, err)
	}
	unstarted.stopped = true
	result, err = unstarted.CompactLog(context.Background())
	if !errors.Is(err, ErrStopped) || result.Error != ErrStopped.Error() {
		t.Fatalf("stopped CompactLog() = (%+v, %v)", result, err)
	}

	stopping := &Service{cfg: Config{NodeID: 7}, started: true, stopping: true}
	result, err = stopping.CompactLog(context.Background())
	if !errors.Is(err, ErrStopped) || result.SkippedReason != LogCompactionSkipNotStarted {
		t.Fatalf("stopping CompactLog() = (%+v, %v)", result, err)
	}
	unavailable := &Service{cfg: Config{NodeID: 7}, started: true}
	result, err = unavailable.CompactLog(context.Background())
	if !errors.Is(err, ErrNotStarted) || result.Error == "" {
		t.Fatalf("unavailable CompactLog() = (%+v, %v)", result, err)
	}

	compactCh := make(chan compactRequest)
	service := &Service{
		cfg: Config{NodeID: 7}, started: true, compact: compactCh,
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	want := LogCompactionResult{NodeID: 7, AppliedIndex: 42, Compacted: true, AfterSnapshotIndex: 42}
	go func() {
		req := <-compactCh
		if req.ctx == nil {
			panic("compact request context is nil")
		}
		req.resp <- compactResponse{result: want}
	}()
	result, err = service.CompactLog(context.Background())
	if err != nil || result != want {
		t.Fatalf("active CompactLog() = (%+v, %v)", result, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	blocked := &Service{
		cfg: Config{NodeID: 7}, started: true, compact: make(chan compactRequest),
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	if _, err := blocked.CompactLog(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send CompactLog() error = %v", err)
	}

	failed := errors.New("compaction loop failed")
	done := make(chan struct{})
	close(done)
	blocked.doneCh = done
	blocked.err = failed
	if _, err := blocked.CompactLog(context.Background()); !errors.Is(err, failed) {
		t.Fatalf("failed loop CompactLog() error = %v", err)
	}

	blocked.doneCh = make(chan struct{})
	stopped := make(chan struct{})
	close(stopped)
	blocked.stopCh = stopped
	if _, err := blocked.CompactLog(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("stopped send CompactLog() error = %v", err)
	}

	waitCtx, waitCancel := context.WithCancel(context.Background())
	received := make(chan struct{})
	waiting := &Service{
		cfg: Config{NodeID: 7}, started: true, compact: make(chan compactRequest),
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	go func() {
		<-waiting.compact
		close(received)
	}()
	go func() {
		<-received
		waitCancel()
	}()
	if _, err := waiting.CompactLog(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled response CompactLog() error = %v", err)
	}
}

func TestStepReportsBackpressureAndShutdownBoundaries(t *testing.T) {
	observer := &serviceObserverCapture{}
	fullQueue := func() chan raftpb.Message {
		ch := make(chan raftpb.Message, 1)
		ch <- raftpb.Message{To: 7, Type: raftpb.MsgHeartbeat}
		return ch
	}

	stopping := &Service{cfg: Config{NodeID: 7}, started: true, stopping: true}
	if err := stopping.Step(context.Background(), raftpb.Message{To: 7}); !errors.Is(err, ErrStopped) {
		t.Fatalf("stopping Step() error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	service := &Service{
		cfg: Config{NodeID: 7, Observer: observer}, started: true, stepCh: fullQueue(),
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	if err := service.Step(canceled, raftpb.Message{To: 7}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Step() error = %v", err)
	}
	if observer.result != "err" || observer.depth != 1 || observer.capacity != 1 {
		t.Fatalf("canceled step observation = %+v", observer)
	}

	failed := errors.New("raft loop failed")
	done := make(chan struct{})
	close(done)
	service.stepCh = fullQueue()
	service.doneCh = done
	service.err = failed
	if err := service.Step(context.Background(), raftpb.Message{To: 7}); !errors.Is(err, failed) {
		t.Fatalf("failed-loop Step() error = %v", err)
	}

	stopped := make(chan struct{})
	close(stopped)
	service.stepCh = fullQueue()
	service.doneCh = make(chan struct{})
	service.stopCh = stopped
	if err := service.Step(context.Background(), raftpb.Message{To: 7}); !errors.Is(err, ErrStopped) {
		t.Fatalf("stopped-loop Step() error = %v", err)
	}

	(*Service)(nil).observeStepQueue(nil)
	(*Service)(nil).observeStepEnqueue("ignored", time.Second)
}

func TestLogEntriesRejectsUnavailableAndCanceledInspection(t *testing.T) {
	service := &Service{cfg: Config{NodeID: 7}}
	if _, err := service.LogEntries(nil, LogEntriesOptions{}); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("unstarted LogEntries() error = %v", err)
	}
	service.stopped = true
	if _, err := service.LogEntries(context.Background(), LogEntriesOptions{}); !errors.Is(err, ErrStopped) {
		t.Fatalf("stopped LogEntries() error = %v", err)
	}
	service.started = true
	service.stopped = false
	if _, err := service.LogEntries(context.Background(), LogEntriesOptions{}); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("missing-store LogEntries() error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.LogEntries(canceled, LogEntriesOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled LogEntries() error = %v", err)
	}
}
