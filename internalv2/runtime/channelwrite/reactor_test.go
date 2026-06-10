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
		PrepareWorkers:    1,
		AppendWorkers:     1,
		PostCommitWorkers: 1,
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

func TestEffectWorkerPressureTracksBusyPrepareWorkers(t *testing.T) {
	authorizer := newBlockingAuthorizerForTest()
	observer := newRecordingEffectWorkerPressureObserverForTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:       1,
		MessageID:         newSequenceIDsForPrepare(550),
		Authorizer:        authorizer,
		Appender:          newRecordingAppenderForAppendTest(),
		PrepareWorkers:    1,
		AppendWorkers:     1,
		PostCommitWorkers: 1,
		Observer:          observer,
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
	observer.waitEffectWorkerPressure(t, 0, "prepare", 1, 1)
	result := receiveSubmitResult(t, submitC)
	if result.err != nil {
		t.Fatalf("SubmitLocal() error = %v", result.err)
	}

	authorizer.release()
	requireAppendSuccess(t, waitFutureForTest(t, result.future), 0, 550, 1)
	observer.waitEffectWorkerPressure(t, 0, "prepare", 0, 1)
}

func TestEffectPoolPressureTracksFullPreparePool(t *testing.T) {
	authorizer := newBlockingAuthorizerForTest()
	observer := newRecordingEffectWorkerPressureObserverForTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:       1,
		MailboxSize:       4,
		MessageID:         newSequenceIDsForPrepare(560),
		Authorizer:        authorizer,
		Appender:          newRecordingAppenderForAppendTest(),
		PrepareWorkers:    1,
		AppendWorkers:     1,
		PostCommitWorkers: 1,
		Observer:          observer,
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
		sendItemForPrepare(validPrepareCommand("u2", "room", "second")),
	})
	if err != nil {
		t.Fatalf("second SubmitLocal() error = %v", err)
	}
	observer.waitEffectPoolPressure(t, "prepare", "full", 1, 1, true)

	authorizer.release()
	requireAppendSuccess(t, waitFutureForTest(t, first.future), 0, 560, 1)
	requireAppendSuccess(t, waitFutureForTest(t, secondFuture), 0, 561, 2)
	observer.waitEffectPoolPressure(t, "prepare", "submitted", 1, 1, true)
	observer.waitEffectPoolPressure(t, "prepare", "released", 0, 1, false)
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
		PrepareWorkers:    2,
		AppendWorkers:     2,
		PostCommitWorkers: 2,
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
		PrepareWorkers:    1,
		AppendWorkers:     1,
		PostCommitWorkers: 1,
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

func TestObservePressureWithoutPressureObserverDoesNotTakeStateLock(t *testing.T) {
	observer := &recordingAppendObserverForTest{}
	r := newReactor(
		0,
		1,
		channelStateLimits{},
		newEffectScheduler(effectSchedulerOptions{ReactorCount: 1, PrepareWorkers: 1, AppendWorkers: 1, PostCommitWorkers: 1}),
		preparePorts{},
		appendPorts{observer: observer},
		commitPorts{},
	)
	r.mu.Lock()
	done := make(chan struct{})
	go func() {
		r.observePressure()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		r.mu.Unlock()
		<-done
		t.Fatalf("observePressure blocked on state lock without a pressure observer")
	}
	r.mu.Unlock()
}

func TestObservePressureWithPressureObserverDoesNotTakeStateLock(t *testing.T) {
	observer := &benchmarkPressureObserver{}
	r := newReactor(
		0,
		1,
		channelStateLimits{},
		newEffectScheduler(effectSchedulerOptions{ReactorCount: 1, PrepareWorkers: 1, AppendWorkers: 1, PostCommitWorkers: 1}),
		preparePorts{},
		appendPorts{observer: observer},
		commitPorts{},
	)
	r.mu.Lock()
	done := make(chan struct{})
	go func() {
		r.observePressure()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		r.mu.Unlock()
		<-done
		t.Fatalf("observePressure blocked on state lock with a pressure observer")
	}
	r.mu.Unlock()
}

func TestPressureObservationTracksQueueCounters(t *testing.T) {
	observer := &benchmarkPressureObserver{}
	r := newReactor(
		0,
		16,
		channelStateLimits{pendingItemHighWatermark: 8, appendInflightLimit: 1},
		newEffectScheduler(effectSchedulerOptions{ReactorCount: 1, PrepareWorkers: 1, AppendWorkers: 1, PostCommitWorkers: 1}),
		preparePorts{},
		appendPorts{observer: observer},
		commitPorts{},
	)
	target := AuthorityTarget{ChannelID: ChannelID{ID: "room", Type: 2}, ChannelKey: "2:room", LeaderNodeID: 1}
	key := targetKey(target)
	state := newChannelState(target, r.limits)
	command := validPrepareCommand("u1", "room", "payload")
	item := preparedSend{
		Index:   0,
		Context: context.Background(),
		Command: command,
		future:  newFuture(1),
	}

	r.mu.Lock()
	r.states[key] = state
	state.enqueuePrepared([]preparedSend{item})
	r.addPendingAppendItems(1)
	r.scheduleAppendLocked(key, state)
	r.observePressureLocked()
	r.mu.Unlock()

	if got := observer.last; got.PendingAppendItems != 0 || got.AppendInflightItems != 1 || got.PostCommitBacklog != 0 {
		t.Fatalf("after append schedule pressure = %+v, want pending=0 inflight=1 backlog=0", got)
	}

	r.recordAppendCompletion(appendCompletedEvent{
		key: key,
		seq: 0,
		items: []appendItemCompletion{{
			item:   item,
			result: SendBatchItemResult{Result: SendResult{MessageID: 1, MessageSeq: 10, Reason: ReasonSuccess}},
			appended: AppendBatchItemResult{
				MessageID:  1,
				MessageSeq: 10,
				Message: Message{
					MessageID:   1,
					MessageSeq:  10,
					ChannelID:   command.ChannelID,
					ChannelType: command.ChannelType,
					FromUID:     command.FromUID,
					ClientMsgNo: command.ClientMsgNo,
				},
			},
		}},
	})

	if got := observer.last; got.PendingAppendItems != 0 || got.AppendInflightItems != 0 || got.PostCommitBacklog != 1 {
		t.Fatalf("after append completion pressure = %+v, want pending=0 inflight=0 backlog=1", got)
	}

	r.recordCommitCompletion(commitCompletedEvent{key: key, seq: 0, checkpointSeq: 10})

	if got := observer.last; got.PendingAppendItems != 0 || got.AppendInflightItems != 0 || got.PostCommitBacklog != 0 {
		t.Fatalf("after commit completion pressure = %+v, want pending=0 inflight=0 backlog=0", got)
	}
}

func TestStopCancelsOutstandingPreparePortContexts(t *testing.T) {
	fence := newContextBlockingFenceForTest()
	group := New(Options{
		LocalNodeID:       1,
		MessageID:         newSequenceIDsForPrepare(1000),
		SenderFence:       fence,
		PrepareWorkers:    1,
		AppendWorkers:     1,
		PostCommitWorkers: 1,
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

type effectWorkerPressureKeyForTest struct {
	reactorID int
	stage     string
}

type effectPoolPressureKeyForTest struct {
	stage  string
	result string
}

type recordingEffectWorkerPressureObserverForTest struct {
	mu       sync.Mutex
	events   map[effectWorkerPressureKeyForTest]EffectWorkerPressureObservation
	pools    map[effectPoolPressureKeyForTest]EffectPoolObservation
	changedC chan struct{}
}

func newRecordingEffectWorkerPressureObserverForTest() *recordingEffectWorkerPressureObserverForTest {
	return &recordingEffectWorkerPressureObserverForTest{
		events:   make(map[effectWorkerPressureKeyForTest]EffectWorkerPressureObservation),
		pools:    make(map[effectPoolPressureKeyForTest]EffectPoolObservation),
		changedC: make(chan struct{}),
	}
}

func (o *recordingEffectWorkerPressureObserverForTest) AppendFinished(string, error, time.Duration) {}

func (o *recordingEffectWorkerPressureObserverForTest) SetChannelWriteEffectWorkerPressure(event EffectWorkerPressureObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events[effectWorkerPressureKeyForTest{reactorID: event.ReactorID, stage: event.Stage}] = event
	close(o.changedC)
	o.changedC = make(chan struct{})
}

func (o *recordingEffectWorkerPressureObserverForTest) ObserveChannelWriteEffectPool(event EffectPoolObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pools[effectPoolPressureKeyForTest{stage: event.Stage, result: event.Result}] = event
	close(o.changedC)
	o.changedC = make(chan struct{})
}

func (o *recordingEffectWorkerPressureObserverForTest) waitEffectWorkerPressure(t *testing.T, reactorID int, stage string, inflight int, capacity int) {
	t.Helper()
	deadline := time.After(time.Second)
	key := effectWorkerPressureKeyForTest{reactorID: reactorID, stage: stage}
	for {
		o.mu.Lock()
		event, ok := o.events[key]
		changedC := o.changedC
		o.mu.Unlock()
		if ok && event.WorkerInflight == inflight && event.WorkerCapacity == capacity {
			return
		}
		select {
		case <-changedC:
		case <-deadline:
			t.Fatalf("effect worker pressure for reactor=%d stage=%s did not reach inflight=%d capacity=%d; last=%+v ok=%v", reactorID, stage, inflight, capacity, event, ok)
		}
	}
}

func (o *recordingEffectWorkerPressureObserverForTest) waitEffectPoolPressure(t *testing.T, stage string, result string, inflight int, capacity int, saturated bool) {
	t.Helper()
	deadline := time.After(time.Second)
	key := effectPoolPressureKeyForTest{stage: stage, result: result}
	for {
		o.mu.Lock()
		event, ok := o.pools[key]
		changedC := o.changedC
		o.mu.Unlock()
		if ok && event.Inflight == inflight && event.Capacity == capacity && event.Saturated == saturated {
			return
		}
		select {
		case <-changedC:
		case <-deadline:
			t.Fatalf("effect pool pressure for stage=%s result=%s did not reach inflight=%d capacity=%d saturated=%v; last=%+v ok=%v", stage, result, inflight, capacity, saturated, event, ok)
		}
	}
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
