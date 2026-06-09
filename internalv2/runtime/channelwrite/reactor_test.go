package channelwrite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReactorRunsPrepareAsWorkerEffect(t *testing.T) {
	authorizer := newBlockingAuthorizerForTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:       1,
		MessageID:         newSequenceIDsForPrepare(500),
		Authorizer:        authorizer,
		Appender:          newRecordingAppenderForAppendTest(),
		EffectWorkerCount: 1,
	})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, ChannelKey: "2:room", LeaderNodeID: 1}

	submitC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
			sendItemForPrepare(validPrepareCommand("u1", "room", "payload")),
		})
		submitC <- submitResult{future: future, err: err}
	}()

	authorizer.waitStarted(t)
	result := receiveSubmitResult(t, submitC)
	if result.err != nil {
		t.Fatalf("SubmitLocal() error = %v", result.err)
	}
	if result.future == nil {
		t.Fatalf("future is nil")
	}

	notDoneCtx, cancelNotDone := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelNotDone()
	if _, err := result.future.Wait(notDoneCtx); err != context.DeadlineExceeded {
		t.Fatalf("Future.Wait() before effect release error = %v, want context deadline", err)
	}

	authorizer.release()
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	results, err := result.future.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Future.Wait() error = %v", err)
	}
	requireAppendSuccess(t, results, 0, 500, 1)
}

func TestReactorPreservesSameChannelOrderAcrossConcurrentPrepareWorkers(t *testing.T) {
	authorizer := newPayloadBlockingAuthorizerForTest("first")
	appender := newRecordingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:       1,
		MailboxSize:       4,
		MessageID:         newSequenceIDsForPrepare(700),
		Authorizer:        authorizer,
		Appender:          appender,
		EffectWorkerCount: 2,
	})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, ChannelKey: "2:room", LeaderNodeID: 1}

	firstC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
			sendItemForPrepare(validPrepareCommand("u1", "room", "first")),
		})
		firstC <- submitResult{future: future, err: err}
	}()
	authorizer.waitBlocked(t)
	first := receiveSubmitResult(t, firstC)
	if first.err != nil {
		t.Fatalf("first SubmitLocal() error = %v", first.err)
	}

	secondFuture, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		sendItemForPrepare(validPrepareCommand("u1", "room", "second")),
	})
	if err != nil {
		t.Fatalf("second SubmitLocal() error = %v", err)
	}
	secondWaitC := make(chan futureWaitResultForTest, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		results, err := secondFuture.Wait(ctx)
		secondWaitC <- futureWaitResultForTest{results: results, err: err}
	}()

	select {
	case <-secondWaitC:
		t.Fatalf("second future completed before first same-channel prepare")
	case <-time.After(20 * time.Millisecond):
	}

	authorizer.release()
	firstResults := waitFutureForTest(t, first.future)
	requireAppendSuccessAnyID(t, firstResults, 0)
	select {
	case second := <-secondWaitC:
		if second.err != nil {
			t.Fatalf("second Future.Wait() error = %v", second.err)
		}
		requireAppendSuccessAnyID(t, second.results, 0)
	case <-time.After(time.Second):
		t.Fatalf("second future did not complete after first prepare released")
	}

	requests := appender.Requests()
	if len(requests) != 2 {
		t.Fatalf("append requests = %d, want 2", len(requests))
	}
	if got := string(requests[0].Messages[0].Payload); got != "first" {
		t.Fatalf("append[0] payload = %q, want first", got)
	}
	if got := string(requests[1].Messages[0].Payload); got != "second" {
		t.Fatalf("append[1] payload = %q, want second", got)
	}
}

func TestSubmitLocalReturnsBackpressureWhenPrepareEffectsAreFull(t *testing.T) {
	authorizer := newBlockingAuthorizerForTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:       1,
		MailboxSize:       1,
		MessageID:         newSequenceIDsForPrepare(900),
		Authorizer:        authorizer,
		Appender:          newRecordingAppenderForAppendTest(),
		EffectWorkerCount: 1,
	})
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, ChannelKey: "2:room", LeaderNodeID: 1}

	firstC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
			sendItemForPrepare(validPrepareCommand("u1", "room", "first")),
		})
		firstC <- submitResult{future: future, err: err}
	}()
	authorizer.waitStarted(t)
	first := receiveSubmitResult(t, firstC)
	if first.err != nil {
		t.Fatalf("first SubmitLocal() error = %v", first.err)
	}

	secondFuture, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		sendItemForPrepare(validPrepareCommand("u1", "room", "second")),
	})
	if !errors.Is(err, ErrBackpressured) {
		authorizer.release()
		if secondFuture != nil {
			_ = waitFutureForTest(t, secondFuture)
		}
		_ = waitFutureForTest(t, first.future)
		t.Fatalf("second SubmitLocal() error = %v, want ErrBackpressured", err)
	}
	if secondFuture != nil {
		authorizer.release()
		_ = waitFutureForTest(t, first.future)
		t.Fatalf("second future = %v, want nil", secondFuture)
	}

	authorizer.release()
	requireAppendSuccess(t, waitFutureForTest(t, first.future), 0, 900, 1)
}

func TestStopCancelsOutstandingPreparePortContexts(t *testing.T) {
	fence := newContextBlockingFenceForTest()
	group := New(Options{
		LocalNodeID:       1,
		MessageID:         newSequenceIDsForPrepare(1000),
		SenderFence:       fence,
		EffectWorkerCount: 1,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, ChannelKey: "2:room", LeaderNodeID: 1}

	submitC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
			sendItemForPrepare(validPrepareCommand("u1", "room", "payload")),
		})
		submitC <- submitResult{future: future, err: err}
	}()
	fence.waitStarted(t)
	submit := receiveSubmitResult(t, submitC)
	if submit.err != nil {
		t.Fatalf("SubmitLocal() error = %v", submit.err)
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := group.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !fence.wasCanceled() {
		t.Fatalf("sender fence did not observe runtime cancellation")
	}
	results := waitFutureForTest(t, submit.future)
	if !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("result error = %v, want context.Canceled", results[0].Err)
	}
}

type blockingAuthorizerForTest struct {
	started  chan struct{}
	releaseC chan struct{}
	once     sync.Once
}

type futureWaitResultForTest struct {
	results []SendBatchItemResult
	err     error
}

func newBlockingAuthorizerForTest() *blockingAuthorizerForTest {
	return &blockingAuthorizerForTest{
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (a *blockingAuthorizerForTest) AuthorizeSend(context.Context, SendCommand) (Decision, error) {
	a.once.Do(func() {
		close(a.started)
	})
	<-a.releaseC
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}

func (a *blockingAuthorizerForTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatalf("authorizer did not start")
	}
}

func (a *blockingAuthorizerForTest) release() {
	close(a.releaseC)
}

type payloadBlockingAuthorizerForTest struct {
	payload  string
	started  chan struct{}
	releaseC chan struct{}
	once     sync.Once
}

func newPayloadBlockingAuthorizerForTest(payload string) *payloadBlockingAuthorizerForTest {
	return &payloadBlockingAuthorizerForTest{
		payload:  payload,
		started:  make(chan struct{}),
		releaseC: make(chan struct{}),
	}
}

func (a *payloadBlockingAuthorizerForTest) AuthorizeSend(_ context.Context, cmd SendCommand) (Decision, error) {
	if string(cmd.Payload) == a.payload {
		a.once.Do(func() {
			close(a.started)
		})
		<-a.releaseC
	}
	return Decision{Allowed: true, Reason: ReasonSuccess}, nil
}

func (a *payloadBlockingAuthorizerForTest) waitBlocked(t *testing.T) {
	t.Helper()
	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatalf("authorizer did not block target payload")
	}
}

func (a *payloadBlockingAuthorizerForTest) release() {
	close(a.releaseC)
}

type contextBlockingFenceForTest struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newContextBlockingFenceForTest() *contextBlockingFenceForTest {
	return &contextBlockingFenceForTest{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

func (f *contextBlockingFenceForTest) ValidateSender(ctx context.Context, _ SendCommand) error {
	f.once.Do(func() {
		close(f.started)
	})
	<-ctx.Done()
	close(f.canceled)
	return ctx.Err()
}

func (f *contextBlockingFenceForTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatalf("sender fence did not start")
	}
}

func (f *contextBlockingFenceForTest) wasCanceled() bool {
	select {
	case <-f.canceled:
		return true
	default:
		return false
	}
}
