package benchterminal_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/benchterminal"
)

func TestPrepareDrainsInStrictPipelineOrderBeforeGrant(t *testing.T) {
	steps := &recordedSteps{}
	terminal := benchterminal.New(benchterminal.Options{
		Gateway:       steps.gateway(),
		ChannelAppend: steps.channelAppend(),
		Delivery:      steps.delivery(),
	})
	grant, err := terminal.Prepare(context.Background(), prepareRequest(2))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if grant.Epoch == 0 || grant.Capability == "" {
		t.Fatalf("Prepare() grant = %#v, want non-zero epoch and capability", grant)
	}
	if got, want := steps.snapshot(), []string{"gateway", "channelappend", "delivery"}; !equalStrings(got, want) {
		t.Fatalf("drain order = %v, want %v", got, want)
	}
	status := terminal.Status()
	if status.Stage != benchterminal.StageAwaitingSessions || status.Epoch != grant.Epoch || status.ExpectedSessions != 2 || status.SealedSessions != 0 || status.Failure != benchterminal.FailureNone {
		t.Fatalf("Status() = %#v, want awaiting bounded epoch/count status", status)
	}
}

func TestPrepareCallerCancellationDoesNotCancelAcceptedPipeline(t *testing.T) {
	steps := &recordedSteps{gatewayStarted: make(chan struct{}), gatewayRelease: make(chan struct{})}
	terminal := benchterminal.New(benchterminal.Options{
		Gateway:       steps.blockingGateway(),
		ChannelAppend: steps.channelAppend(),
		Delivery:      steps.delivery(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := terminal.Prepare(ctx, prepareRequest(1))
		firstDone <- err
	}()
	<-steps.gatewayStarted
	cancel()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Prepare() error = %v, want caller cancellation", err)
	}
	close(steps.gatewayRelease)
	grant, err := terminal.Prepare(context.Background(), prepareRequest(1))
	if err != nil {
		t.Fatalf("second Prepare() error = %v, want background pipeline completion", err)
	}
	if grant.Epoch == 0 {
		t.Fatal("second Prepare() returned zero epoch")
	}
	if got, want := steps.snapshot(), []string{"gateway", "channelappend", "delivery"}; !equalStrings(got, want) {
		t.Fatalf("pipeline after canceled caller = %v, want %v", got, want)
	}
}

func TestPrepareFailureNeverGrantsAndIsPermanent(t *testing.T) {
	boom := errors.New("channel append unavailable")
	steps := &recordedSteps{channelAppendErr: boom}
	terminal := benchterminal.New(benchterminal.Options{
		Gateway:       steps.gateway(),
		ChannelAppend: steps.channelAppend(),
		Delivery:      steps.delivery(),
	})
	grant, err := terminal.Prepare(context.Background(), prepareRequest(1))
	if !errors.Is(err, benchterminal.ErrPreparationFailed) || grant.Epoch != 0 || grant.Capability != "" {
		t.Fatalf("Prepare() = (%#v, %v), want empty grant and permanent failure", grant, err)
	}
	if got, want := steps.snapshot(), []string{"gateway", "channelappend"}; !equalStrings(got, want) {
		t.Fatalf("failed pipeline = %v, want %v", got, want)
	}
	if status := terminal.Status(); status.Stage != benchterminal.StageFailed || status.Failure != benchterminal.FailureChannelAppendStop {
		t.Fatalf("Status() = %#v, want channelappend failure", status)
	}
	if _, err := terminal.Prepare(context.Background(), prepareRequest(1)); !errors.Is(err, benchterminal.ErrPreparationFailed) {
		t.Fatalf("same identity retry error = %v, want permanent preparation failure", err)
	}
	if _, err := terminal.Prepare(context.Background(), benchterminal.PrepareRequest{RunID: "other", AssignmentID: "assignment-1", ExpectedSessions: 1}); !errors.Is(err, benchterminal.ErrPreparationConflict) {
		t.Fatalf("different identity retry error = %v, want conflict", err)
	}
}

func TestPrepareHasDetachedHardDeadline(t *testing.T) {
	terminal := benchterminal.New(benchterminal.Options{
		Gateway: drainFunc(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		ChannelAppend: stopFunc(func(context.Context) error { t.Fatal("channelappend must not run after gateway deadline"); return nil }),
		Delivery:      quiesceFunc(func(context.Context) error { t.Fatal("delivery must not run after gateway deadline"); return nil }),
		DrainTimeout:  10 * time.Millisecond,
	})
	grant, err := terminal.Prepare(context.Background(), prepareRequest(1))
	if !errors.Is(err, benchterminal.ErrPreparationFailed) || grant.Epoch != 0 {
		t.Fatalf("Prepare() = (%#v, %v), want deadline-bound permanent failure", grant, err)
	}
	if status := terminal.Status(); status.Failure != benchterminal.FailureGatewayDrain {
		t.Fatalf("Status() = %#v, want gateway drain failure", status)
	}
}

func TestPrepareIsConcurrentAndIdentityIdempotent(t *testing.T) {
	steps := &recordedSteps{gatewayStarted: make(chan struct{}), gatewayRelease: make(chan struct{})}
	terminal := benchterminal.New(benchterminal.Options{
		Gateway:       steps.blockingGateway(),
		ChannelAppend: steps.channelAppend(),
		Delivery:      steps.delivery(),
	})
	type result struct {
		grant benchterminal.Grant
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			grant, err := terminal.Prepare(context.Background(), prepareRequest(1))
			results <- result{grant: grant, err: err}
		}()
	}
	<-steps.gatewayStarted
	close(steps.gatewayRelease)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.grant != second.grant {
		t.Fatalf("concurrent Prepare() = (%#v, %v), (%#v, %v), want one identical grant", first.grant, first.err, second.grant, second.err)
	}
	if got, want := steps.snapshot(), []string{"gateway", "channelappend", "delivery"}; !equalStrings(got, want) {
		t.Fatalf("idempotent pipeline calls = %v, want %v", got, want)
	}
}

func TestSealAndEnqueueCompletesOnlyTheExactSessionSet(t *testing.T) {
	sealer := &recordingSealer{}
	terminal, grant := readyTerminal(t, 2)

	proof := benchterminal.ProofForGrant(grant)
	first, err := terminal.SealAndEnqueue(context.Background(), proof, fence(10, grant.Epoch, 1), sealer)
	if err != nil || !first.Enqueued || first.Complete || first.SealedSessions != 1 {
		t.Fatalf("first SealAndEnqueue() = (%#v, %v), want one enqueued non-complete session", first, err)
	}
	second, err := terminal.SealAndEnqueue(context.Background(), proof, fence(11, grant.Epoch, 3), sealer)
	if err != nil || !second.Enqueued || !second.Complete || second.SealedSessions != 2 {
		t.Fatalf("second SealAndEnqueue() = (%#v, %v), want complete session set", second, err)
	}
	if got, want := sealer.sessionIDs(), []uint64{10, 11}; !equalUint64s(got, want) {
		t.Fatalf("sealed session IDs = %v, want %v", got, want)
	}
	if status := terminal.Status(); status.Stage != benchterminal.StageSessionsSealed || status.SealedSessions != 2 || status.ExpectedSessions != 2 || status.Epoch != grant.Epoch || status.Failure != benchterminal.FailureNone {
		t.Fatalf("Status() = %#v, want bounded complete state", status)
	}
}

func TestSealAndEnqueueProtocolViolationsPermanentlyFailPublishedEpoch(t *testing.T) {
	tests := []struct {
		name string
		act  func(*benchterminal.Controller, benchterminal.Grant, *recordingSealer) error
		want error
	}{
		{
			name: "wrong capability proof",
			act: func(terminal *benchterminal.Controller, grant benchterminal.Grant, sealer *recordingSealer) error {
				proof := benchterminal.ProofForGrant(grant)
				proof.CapabilitySHA256[0] ^= 0xff
				_, err := terminal.SealAndEnqueue(context.Background(), proof, fence(10, grant.Epoch, 1), sealer)
				return err
			},
			want: benchterminal.ErrGrantRejected,
		},
		{
			name: "stale fence epoch",
			act: func(terminal *benchterminal.Controller, grant benchterminal.Grant, sealer *recordingSealer) error {
				_, err := terminal.SealAndEnqueue(context.Background(), benchterminal.ProofForGrant(grant), fence(10, grant.Epoch+1, 1), sealer)
				return err
			},
			want: benchterminal.ErrGrantRejected,
		},
		{
			name: "duplicate session",
			act: func(terminal *benchterminal.Controller, grant benchterminal.Grant, sealer *recordingSealer) error {
				proof := benchterminal.ProofForGrant(grant)
				if _, err := terminal.SealAndEnqueue(context.Background(), proof, fence(10, grant.Epoch, 1), sealer); err != nil {
					return err
				}
				_, err := terminal.SealAndEnqueue(context.Background(), proof, fence(10, grant.Epoch, 2), sealer)
				return err
			},
			want: benchterminal.ErrProtocolViolation,
		},
		{
			name: "session above exact count",
			act: func(terminal *benchterminal.Controller, grant benchterminal.Grant, sealer *recordingSealer) error {
				proof := benchterminal.ProofForGrant(grant)
				if _, err := terminal.SealAndEnqueue(context.Background(), proof, fence(10, grant.Epoch, 1), sealer); err != nil {
					return err
				}
				_, err := terminal.SealAndEnqueue(context.Background(), proof, fence(11, grant.Epoch, 2), sealer)
				return err
			},
			want: benchterminal.ErrSessionLimit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			terminal, grant := readyTerminal(t, 1)
			sealer := &recordingSealer{}
			if err := test.act(terminal, grant, sealer); !errors.Is(err, test.want) {
				t.Fatalf("protocol violation error = %v, want %v", err, test.want)
			}
			if status := terminal.Status(); status.Stage != benchterminal.StageFailed || status.Failure != benchterminal.FailureProtocolViolation || status.Epoch != grant.Epoch {
				t.Fatalf("Status() = %#v, want permanent protocol failure", status)
			}
			if err := terminal.ValidateGrant(grant); !errors.Is(err, benchterminal.ErrPreparationFailed) {
				t.Fatalf("ValidateGrant() after violation = %v, want preparation failure", err)
			}
		})
	}
}

func TestSessionSealErrorPermanentlyFailsEpoch(t *testing.T) {
	terminal, grant := readyTerminal(t, 1)
	failingSealer := sealFunc(func(context.Context, benchterminal.SessionFence) error {
		return errors.New("marker write failed")
	})
	if _, err := terminal.SealAndEnqueue(context.Background(), benchterminal.ProofForGrant(grant), fence(10, grant.Epoch, 1), failingSealer); !errors.Is(err, benchterminal.ErrPreparationFailed) {
		t.Fatalf("SealAndEnqueue() error = %v, want permanent failure", err)
	}
	if status := terminal.Status(); status.Stage != benchterminal.StageFailed || status.Failure != benchterminal.FailureSessionSeal || status.Epoch != grant.Epoch {
		t.Fatalf("Status() = %#v, want terminal session seal failure", status)
	}
	if err := terminal.ValidateGrant(grant); !errors.Is(err, benchterminal.ErrPreparationFailed) {
		t.Fatalf("ValidateGrant() after session failure = %v, want preparation failure", err)
	}
}

func TestGrantAndStatusDoNotExposeCapability(t *testing.T) {
	secret := "this-secret-must-not-appear-in-formatting"
	grant := benchterminal.Grant{Epoch: 7, Capability: secret}
	if formatted := fmt.Sprintf("%v %#v", grant, grant); strings.Contains(formatted, secret) {
		t.Fatalf("Grant formatting leaked capability: %q", formatted)
	}
	fence := benchterminal.SessionFence{SessionID: 8, Epoch: 9, Nonce: [16]byte{42}}
	if formatted := fmt.Sprintf("%v %#v", fence, fence); strings.Contains(formatted, "42") {
		t.Fatalf("SessionFence formatting leaked nonce: %q", formatted)
	}
	terminal := benchterminal.New(benchterminal.Options{
		Gateway:       drainFunc(func(context.Context) error { return nil }),
		ChannelAppend: stopFunc(func(context.Context) error { return nil }),
		Delivery:      quiesceFunc(func(context.Context) error { return nil }),
		Reader:        bytes.NewReader(append(nonZeroEpochBytes(), bytes.Repeat([]byte{1}, 32)...)),
	})
	issued, err := terminal.Prepare(context.Background(), prepareRequest(1))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if formatted := fmt.Sprintf("%#v", terminal.Status()); strings.Contains(formatted, issued.Capability) {
		t.Fatalf("Status formatting leaked capability: %q", formatted)
	}
}

func TestPrepareRejectsRepeatedZeroRandomEpoch(t *testing.T) {
	terminal := benchterminal.New(benchterminal.Options{
		Gateway:       drainFunc(func(context.Context) error { return nil }),
		ChannelAppend: stopFunc(func(context.Context) error { return nil }),
		Delivery:      quiesceFunc(func(context.Context) error { return nil }),
		Reader:        bytes.NewReader(make([]byte, 8*3)),
	})
	if _, err := terminal.Prepare(context.Background(), prepareRequest(1)); !errors.Is(err, benchterminal.ErrPreparationFailed) {
		t.Fatalf("Prepare() error = %v, want failed zero-epoch grant", err)
	}
	if status := terminal.Status(); status.Failure != benchterminal.FailureRandom || status.Epoch != 0 {
		t.Fatalf("Status() = %#v, want random failure without epoch", status)
	}
}

func TestPrepareUsesTargetIdentityAndSessionBounds(t *testing.T) {
	terminal := benchterminal.New(benchterminal.Options{MaxSessions: 2})
	for _, request := range []benchterminal.PrepareRequest{
		{RunID: strings.Repeat("r", 129), AssignmentID: "assignment", ExpectedSessions: 1},
		{RunID: "run", AssignmentID: strings.Repeat("a", 129), ExpectedSessions: 1},
		{RunID: "run", AssignmentID: "assignment", ExpectedSessions: 1_000_001},
		{RunID: "run", AssignmentID: "assignment", ExpectedSessions: 3},
	} {
		if _, err := terminal.Prepare(context.Background(), request); !errors.Is(err, benchterminal.ErrInvalidPrepareRequest) {
			t.Fatalf("Prepare(%#v) error = %v, want target/controller bound rejection", request, err)
		}
	}
}

func readyTerminal(t *testing.T, expected int) (*benchterminal.Controller, benchterminal.Grant) {
	t.Helper()
	terminal := benchterminal.New(benchterminal.Options{
		Gateway:       drainFunc(func(context.Context) error { return nil }),
		ChannelAppend: stopFunc(func(context.Context) error { return nil }),
		Delivery:      quiesceFunc(func(context.Context) error { return nil }),
	})
	grant, err := terminal.Prepare(context.Background(), prepareRequest(expected))
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return terminal, grant
}

func prepareRequest(expected int) benchterminal.PrepareRequest {
	return benchterminal.PrepareRequest{RunID: "run-1", AssignmentID: "assignment-1", ExpectedSessions: expected}
}

func fence(sessionID, epoch uint64, nonce byte) benchterminal.SessionFence {
	return benchterminal.SessionFence{SessionID: sessionID, Epoch: epoch, Nonce: [16]byte{nonce}}
}

func nonZeroEpochBytes() []byte {
	return []byte{0, 0, 0, 0, 0, 0, 0, 1}
}

type recordedSteps struct {
	mu               sync.Mutex
	steps            []string
	gatewayStarted   chan struct{}
	gatewayRelease   chan struct{}
	gatewayStartOnce sync.Once
	channelAppendErr error
}

func (s *recordedSteps) gateway() benchterminal.GatewayDrainer {
	return drainFunc(func(context.Context) error { s.add("gateway"); return nil })
}

func (s *recordedSteps) blockingGateway() benchterminal.GatewayDrainer {
	return drainFunc(func(context.Context) error {
		s.add("gateway")
		s.gatewayStartOnce.Do(func() { close(s.gatewayStarted) })
		<-s.gatewayRelease
		return nil
	})
}

func (s *recordedSteps) channelAppend() benchterminal.ChannelAppendStopper {
	return stopFunc(func(context.Context) error { s.add("channelappend"); return s.channelAppendErr })
}

func (s *recordedSteps) delivery() benchterminal.DeliveryQuiescer {
	return quiesceFunc(func(context.Context) error { s.add("delivery"); return nil })
}

func (s *recordedSteps) add(step string) {
	s.mu.Lock()
	s.steps = append(s.steps, step)
	s.mu.Unlock()
}

func (s *recordedSteps) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

type drainFunc func(context.Context) error

func (f drainFunc) DrainSends(ctx context.Context) error { return f(ctx) }

type stopFunc func(context.Context) error

func (f stopFunc) Stop(ctx context.Context) error { return f(ctx) }

type quiesceFunc func(context.Context) error

func (f quiesceFunc) Quiesce(ctx context.Context) error { return f(ctx) }

type sealFunc func(context.Context, benchterminal.SessionFence) error

func (f sealFunc) SealAndEnqueue(ctx context.Context, fence benchterminal.SessionFence) error {
	return f(ctx, fence)
}

type recordingSealer struct {
	mu     sync.Mutex
	fences []benchterminal.SessionFence
}

func (s *recordingSealer) SealAndEnqueue(_ context.Context, fence benchterminal.SessionFence) error {
	s.mu.Lock()
	s.fences = append(s.fences, fence)
	s.mu.Unlock()
	return nil
}

func (s *recordingSealer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.fences)
}

func (s *recordingSealer) sessionIDs() []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]uint64, len(s.fences))
	for index := range s.fences {
		ids[index] = s.fences[index].SessionID
	}
	return ids
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
