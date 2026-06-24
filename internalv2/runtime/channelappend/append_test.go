package channelappend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func TestAppendPreservesOrderWithinOneChannel(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                     1,
		MessageID:                       newSequenceIDsForPrepare(10),
		Appender:                        appender,
		AppendInflightBatchesPerChannel: 1,
		EffectPoolSize:                  4,
	})
	target := localTargetForAppendTest("room")

	firstC := submitNoWaitForAppendTest(group, target, appendSendItemForTest("u1", "room", "first"))
	firstStart := appender.waitStarted(t)
	if got := string(firstStart.Request.Messages[0].Payload); got != "first" {
		t.Fatalf("first append payload = %q, want first", got)
	}

	secondC := submitNoWaitForAppendTest(group, target, appendSendItemForTest("u1", "room", "second"))
	time.Sleep(20 * time.Millisecond)
	if got := appender.Calls(); got != 1 {
		t.Fatalf("append calls while first in-flight = %d, want 1", got)
	}

	firstStart.Release()
	first := receiveSubmitResult(t, firstC)
	requireAppendSuccess(t, waitFutureForTest(t, first.future), 0, 10, 1)

	secondStart := appender.waitStarted(t)
	if got := string(secondStart.Request.Messages[0].Payload); got != "second" {
		t.Fatalf("second append payload = %q, want second", got)
	}
	secondStart.Release()
	second := receiveSubmitResult(t, secondC)
	requireAppendSuccess(t, waitFutureForTest(t, second.future), 0, 11, 2)
}

func TestAppendInflightBatchesPerChannelAboveOneAllowsSecondSameChannelAppend(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:                     1,
		MessageID:                       newSequenceIDsForPrepare(30),
		Appender:                        appender,
		AppendInflightBatchesPerChannel: 2,
		EffectPoolSize:                  4,
	})
	target := localTargetForAppendTest("room")

	firstC := submitNoWaitForAppendTest(group, target, appendSendItemForTest("u1", "room", "first"))
	firstStart := appender.waitStarted(t)

	secondC := submitNoWaitForAppendTest(group, target, appendSendItemForTest("u1", "room", "second"))
	secondStart := appender.waitStarted(t)
	if got := appender.Calls(); got != 2 {
		t.Fatalf("append calls while first same-channel append in-flight = %d, want 2", got)
	}

	firstStart.Release()
	first := receiveSubmitResult(t, firstC)
	requireAppendSuccess(t, waitFutureForTest(t, first.future), 0, 30, 1)

	secondStart.Release()
	second := receiveSubmitResult(t, secondC)
	requireAppendSuccess(t, waitFutureForTest(t, second.future), 0, 31, 2)
}

func TestDifferentChannelsAppendIndependentlyOnDifferentShards(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID:         1,
		AuthorityShardCount: 2,
		MessageID:           newSequenceIDsForPrepare(20),
		Appender:            appender,
		EffectPoolSize:      4,
	})
	firstTarget, secondTarget := differentShardTargetsForAppendTest(t, group)

	firstC := submitNoWaitForAppendTest(group, firstTarget, appendSendItemForTest("u1", firstTarget.ChannelID.ID, "first"))
	firstStart := appender.waitStarted(t)

	secondC := submitNoWaitForAppendTest(group, secondTarget, appendSendItemForTest("u2", secondTarget.ChannelID.ID, "second"))
	secondStart := appender.waitStarted(t)
	if secondStart.Request.ChannelID == firstStart.Request.ChannelID {
		t.Fatalf("second append used same channel = %+v", secondStart.Request.ChannelID)
	}

	firstStart.Release()
	first := receiveSubmitResult(t, firstC)
	requireAppendSuccess(t, waitFutureForTest(t, first.future), 0, 20, 1)
	secondStart.Release()
	second := receiveSubmitResult(t, secondC)
	requireAppendSuccess(t, waitFutureForTest(t, second.future), 0, 21, 2)
}

func TestAppendRequestCarriesAuthorityFence(t *testing.T) {
	appender := newRecordingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(90),
		Appender:    appender,
	})
	target := localTargetForAppendTest("room")
	target.Epoch = 123
	target.LeaderEpoch = 456

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "payload"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 90, 1)

	requests := appender.Requests()
	if len(requests) != 1 {
		t.Fatalf("append requests = %d, want 1", len(requests))
	}
	if requests[0].ExpectedEpoch != target.Epoch {
		t.Fatalf("ExpectedEpoch = %d, want %d", requests[0].ExpectedEpoch, target.Epoch)
	}
	if requests[0].ExpectedLeaderEpoch != target.LeaderEpoch {
		t.Fatalf("ExpectedLeaderEpoch = %d, want %d", requests[0].ExpectedLeaderEpoch, target.LeaderEpoch)
	}
}

func TestAppendRequestCarriesLegacyMessageFields(t *testing.T) {
	req := appendRequest(localTargetForAppendTest("room"), []preparedSend{{
		Command: SendCommand{
			FromUID:     "u1",
			ChannelID:   "room",
			ChannelType: 2,
			ClientMsgNo: "client-1",
			Setting:     9,
			Topic:       "topic-a",
			Expire:      3600,
			Payload:     []byte("payload"),
		},
		ServerTimestampMS: 123,
	}}, appendInitialAttempt)

	if len(req.Messages) != 1 {
		t.Fatalf("append messages = %d, want 1", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Setting != 9 || msg.Topic != "topic-a" || msg.Expire != 3600 {
		t.Fatalf("legacy message fields = setting:%d topic:%q expire:%d, want 9/topic-a/3600", msg.Setting, msg.Topic, msg.Expire)
	}
}

func TestCommittedEnvelopeForAppendCarriesLegacyMessageFields(t *testing.T) {
	item := preparedSend{
		Command: SendCommand{
			FromUID:     "u1",
			ChannelID:   "room",
			ChannelType: 2,
			ClientMsgNo: "client-1",
			Setting:     9,
			Topic:       "topic-from-command",
			Expire:      3600,
			Payload:     []byte("command-payload"),
		},
		ServerTimestampMS: 123,
	}
	appended := AppendBatchItemResult{
		MessageID:  10,
		MessageSeq: 2,
		Message: Message{
			Setting:           11,
			Topic:             "topic-from-append",
			Expire:            7200,
			Payload:           []byte("append-payload"),
			ServerTimestampMS: 456,
		},
	}

	event := committedEnvelopeForAppend(item, appended)

	if event.Setting != 11 || event.Topic != "topic-from-append" || event.Expire != 7200 {
		t.Fatalf("legacy envelope fields = setting:%d topic:%q expire:%d, want 11/topic-from-append/7200", event.Setting, event.Topic, event.Expire)
	}
}

func TestCommittedEnvelopeForAppendFallsBackToCommandLegacyFields(t *testing.T) {
	item := preparedSend{
		Command: SendCommand{
			FromUID:     "u1",
			ChannelID:   "room",
			ChannelType: 2,
			ClientMsgNo: "client-1",
			Setting:     9,
			Topic:       "topic-from-command",
			Expire:      3600,
			Payload:     []byte("command-payload"),
		},
		ServerTimestampMS: 123,
	}
	appended := AppendBatchItemResult{MessageID: 10, MessageSeq: 2}

	event := committedEnvelopeForAppend(item, appended)

	if event.Setting != 9 || event.Topic != "topic-from-command" || event.Expire != 3600 {
		t.Fatalf("fallback legacy envelope fields = setting:%d topic:%q expire:%d, want 9/topic-from-command/3600", event.Setting, event.Topic, event.Expire)
	}
}

func TestAppendSuccessCompletesItemAlignedFutures(t *testing.T) {
	appender := newRecordingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(100),
		Appender:    appender,
	})
	target := localTargetForAppendTest("room")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "one"),
		appendSendItemForTest("u2", "room", "two"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	results := waitFutureForTest(t, future)
	requireAppendSuccess(t, results, 0, 100, 1)
	requireAppendSuccess(t, results, 1, 101, 2)
}

func TestShortAppendResultReturnsMissingForMissingItem(t *testing.T) {
	appender := newRecordingAppenderForAppendTest()
	appender.resultLimit = 1
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(200),
		Appender:    appender,
	})
	target := localTargetForAppendTest("room")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "one"),
		appendSendItemForTest("u2", "room", "two"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	results := waitFutureForTest(t, future)
	requireAppendSuccess(t, results, 0, 200, 1)
	if !errors.Is(results[1].Err, ErrAppendResultMissing) {
		t.Fatalf("second result error = %v, want ErrAppendResultMissing", results[1].Err)
	}
}

func TestAppendItemDeadlineDoesNotPoisonSameBatch(t *testing.T) {
	appender := newBlockingAppenderForAppendTest()
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(250),
		Appender:    appender,
	})
	target := localTargetForAppendTest("room")
	first := appendSendItemForTest("u1", "room", "early")
	first.Deadline = time.Now().Add(10 * time.Millisecond)
	second := appendSendItemForTest("u2", "room", "later")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{first, second})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	started := appender.waitStarted(t)
	time.Sleep(30 * time.Millisecond)
	started.Release()

	results := waitFutureForTest(t, future)
	requireAppendSuccess(t, results, 0, 250, 1)
	requireAppendSuccess(t, results, 1, 251, 2)
	if len(started.Request.Messages) != 2 {
		t.Fatalf("append request messages = %d, want both accepted items", len(started.Request.Messages))
	}
	if started.Request.Messages[0].MessageID != 250 || started.Request.Messages[1].MessageID != 251 {
		t.Fatalf("append request ids = %d/%d, want 250/251", started.Request.Messages[0].MessageID, started.Request.Messages[1].MessageID)
	}
}

func TestAppendBatchRouteErrorsFailWithoutRetry(t *testing.T) {
	for _, appendErr := range []error{ErrRouteNotReady, ErrNotLeader, ErrStaleRoute} {
		t.Run(appendErr.Error(), func(t *testing.T) {
			appender := newRecordingAppenderForAppendTest()
			appender.err = appendErr
			group := newStartedTestGroup(t, Options{
				LocalNodeID: 1,
				MessageID:   newSequenceIDsForPrepare(300),
				Appender:    appender,
			})
			target := localTargetForAppendTest("room")

			future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{{
				Context:  context.Background(),
				Deadline: time.Now().Add(time.Second),
				Command:  appendCommandForTest("u1", "room", "fail-once"),
			}})
			if err != nil {
				t.Fatalf("SubmitLocal() error = %v", err)
			}

			results := waitFutureForTest(t, future)
			if !errors.Is(results[0].Err, appendErr) {
				t.Fatalf("result error = %v, want %v", results[0].Err, appendErr)
			}
			if got := appender.Calls(); got != 1 {
				t.Fatalf("append calls = %d, want no retry", got)
			}
		})
	}
}

func TestActiveAppendItemsDoesNotAllocateForAllActiveItems(t *testing.T) {
	items := make([]preparedSend, 16)
	allocs := testing.AllocsPerRun(100, func() {
		active, inactive := activeAppendItems(items)
		if len(active) != len(items) {
			t.Fatalf("active items = %d, want %d", len(active), len(items))
		}
		if len(inactive) != 0 {
			t.Fatalf("inactive items = %d, want 0", len(inactive))
		}
	})
	if allocs != 0 {
		t.Fatalf("activeAppendItems allocations = %.1f, want 0", allocs)
	}
}

func TestActiveAppendItemsKeepsLaterItemsWhenFirstInactive(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	items := []preparedSend{
		{Index: 0, Context: canceled},
		{Index: 1, Context: context.Background()},
	}

	active, inactive := activeAppendItems(items)
	if len(active) != 1 || active[0].Index != 1 {
		t.Fatalf("active items = %+v, want only index 1", active)
	}
	if len(inactive) != 1 || inactive[0].item.Index != 0 || !errors.Is(inactive[0].traceErr, context.Canceled) {
		t.Fatalf("inactive items = %+v, want canceled index 0", inactive)
	}
}

func TestAppendCompletionsResultClassDoesNotAllocate(t *testing.T) {
	items := []appendItemCompletion{
		{result: SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}}},
		{result: SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}}},
	}
	allocs := testing.AllocsPerRun(100, func() {
		if got := appendCompletionsResultClass(items); got != channelAppendResultOK {
			t.Fatalf("result class = %q, want %q", got, channelAppendResultOK)
		}
	})
	if allocs != 0 {
		t.Fatalf("appendCompletionsResultClass allocations = %.1f, want 0", allocs)
	}
}

func TestAppendBatchErrorKeepsPreAppendExpiredItemsSeparate(t *testing.T) {
	appender := newRecordingAppenderForAppendTest()
	appender.err = ErrRouteNotReady
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(350),
		Appender:    appender,
	})
	target := localTargetForAppendTest("room")
	first := appendSendItemForTest("u1", "room", "early")
	first.Deadline = time.Now().Add(-time.Millisecond)
	second := appendSendItemForTest("u2", "room", "later")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{first, second})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	results := waitFutureForTest(t, future)
	if !errors.Is(results[0].Err, context.DeadlineExceeded) {
		t.Fatalf("first result error = %v, want DeadlineExceeded", results[0].Err)
	}
	if !errors.Is(results[1].Err, ErrRouteNotReady) {
		t.Fatalf("second result error = %v, want ErrRouteNotReady", results[1].Err)
	}
	requests := appender.Requests()
	if len(requests) != 1 {
		t.Fatalf("append requests = %d, want single attempt", len(requests))
	}
	if len(requests[0].Messages) != 1 || requests[0].Messages[0].ClientMsgNo != "u2-later" {
		t.Fatalf("append request messages = %#v, want only later item", requests[0].Messages)
	}
}

func TestAppendSuccessPrunesNoopCommitWithoutRecipientEffects(t *testing.T) {
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(400),
		Appender:    newRecordingAppenderForAppendTest(),
	})
	target := localTargetForAppendTest("room")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("u1", "room", "payload"),
	})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 400, 1)
	waitCommitBacklogForTest(t, group, target.ChannelID, 0)
}

func TestAppendSharesImmutablePayloadWithAppender(t *testing.T) {
	payload := []byte("hello")
	appender := &payloadAliasAppenderForAppendTest{payload: payload}
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(500),
		Appender:    appender,
	})
	target := localTargetForAppendTest("room")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:     "u1",
			ChannelID:   "room",
			ChannelType: 2,
			ClientMsgNo: "client-1",
			Payload:     payload,
		},
	}})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 500, 1)
	if !appender.sawAlias {
		t.Fatalf("append request payload did not share immutable command payload")
	}
}

func TestAppendRecordsObserverAndSendtraceFromCompletion(t *testing.T) {
	sink := &recordingSendtraceSinkForAppendTest{}
	restore := sendtrace.SetSink(sink)
	t.Cleanup(restore)
	observer := &recordingAppendObserverForTest{}
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(700),
		Appender:    newRecordingAppenderForAppendTest(),
		Observer:    observer,
	})
	target := localTargetForAppendTest("room")

	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{{
		Context: context.Background(),
		Command: SendCommand{
			FromUID:      "u1",
			SenderNodeID: 7,
			ClientMsgNo:  "client-1",
			TraceID:      "trace-1",
			ChannelKey:   "channel/key-1",
			ChannelID:    "room",
			ChannelType:  2,
			Payload:      []byte("payload"),
		},
	}})
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}

	requireAppendSuccess(t, waitFutureForTest(t, future), 0, 700, 1)
	events := observer.Events()
	if len(events) != 1 {
		t.Fatalf("observer events = %d, want 1", len(events))
	}
	if events[0].path != appendMetricPathChannelPlane || events[0].err != nil {
		t.Fatalf("observer event = %#v, want channelplane success", events[0])
	}
	traceEvents := sink.snapshot()
	if len(traceEvents) != 1 {
		t.Fatalf("sendtrace events = %#v, want 1", traceEvents)
	}
	event := traceEvents[0]
	if event.Stage != sendtrace.StageMessageSendDurable {
		t.Fatalf("stage = %q, want %q", event.Stage, sendtrace.StageMessageSendDurable)
	}
	if event.TraceID != "trace-1" || event.ChannelKey != "channel/key-1" || event.ClientMsgNo != "client-1" || event.FromUID != "u1" {
		t.Fatalf("trace fields = %#v", event)
	}
	if event.NodeID != 7 || event.MessageSeq != 1 || event.Result != sendtrace.ResultOK || event.ErrorCode != "" {
		t.Fatalf("trace outcome = node %d seq %d result %q/%q, want 7 seq 1 ok",
			event.NodeID, event.MessageSeq, event.Result, event.ErrorCode)
	}
}

func TestStopCancelsBlockedAppendWorkerAndFuture(t *testing.T) {
	appender := newContextBlockingAppenderForAppendTest()
	group := New(Options{
		LocalNodeID: 1,
		MessageID:   newSequenceIDsForPrepare(800),
		Appender:    appender,
	})
	if err := group.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	target := localTargetForAppendTest("room")

	submitC := submitNoWaitForAppendTest(group, target, appendSendItemForTest("u1", "room", "payload"))
	appender.waitStarted(t)
	submit := receiveSubmitResult(t, submitC)
	if submit.err != nil {
		t.Fatalf("SubmitLocal() error = %v", submit.err)
	}

	stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := group.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !appender.wasCanceled() {
		t.Fatalf("appender did not observe context cancellation")
	}
	results := waitFutureForTest(t, submit.future)
	if !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("future error = %v, want context.Canceled", results[0].Err)
	}
}

type recordingAppenderForAppendTest struct {
	mu          sync.Mutex
	calls       int
	requests    []AppendBatchRequest
	err         error
	errs        []error
	itemErrs    []error
	resultLimit int
	nextSeq     uint64
}

func newRecordingAppenderForAppendTest() *recordingAppenderForAppendTest {
	return &recordingAppenderForAppendTest{nextSeq: 1}
}

func (a *recordingAppenderForAppendTest) AppendBatch(_ context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	a.mu.Lock()
	a.calls++
	call := a.calls
	a.requests = append(a.requests, req.Clone())
	err := a.err
	if call <= len(a.errs) {
		err = a.errs[call-1]
	}
	itemErrs := append([]error(nil), a.itemErrs...)
	resultLimit := a.resultLimit
	a.mu.Unlock()
	if err != nil {
		return AppendBatchResult{}, err
	}
	return a.successResult(req, itemErrs, resultLimit), nil
}

func (a *recordingAppenderForAppendTest) successResult(req AppendBatchRequest, itemErrs []error, resultLimit int) AppendBatchResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	itemCount := len(req.Messages)
	if resultLimit > 0 && resultLimit < itemCount {
		itemCount = resultLimit
	}
	items := make([]AppendBatchItemResult, itemCount)
	for i, msg := range req.Messages[:itemCount] {
		msg.MessageSeq = a.nextSeq
		if req.OmitResultPayload {
			msg.Payload = nil
		}
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
		if i < len(itemErrs) {
			items[i].Err = itemErrs[i]
		}
		a.nextSeq++
	}
	return AppendBatchResult{Items: items}
}

func (a *recordingAppenderForAppendTest) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *recordingAppenderForAppendTest) Requests() []AppendBatchRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := append([]AppendBatchRequest(nil), a.requests...)
	for i := range out {
		out[i] = out[i].Clone()
	}
	return out
}

type blockingAppenderForAppendTest struct {
	mu       sync.Mutex
	calls    int
	nextSeq  uint64
	startedC chan appendStartedForAppendTest
}

type appendStartedForAppendTest struct {
	Request AppendBatchRequest
	release chan struct{}
}

func newBlockingAppenderForAppendTest() *blockingAppenderForAppendTest {
	return &blockingAppenderForAppendTest{
		nextSeq:  1,
		startedC: make(chan appendStartedForAppendTest, 16),
	}
}

func (a *blockingAppenderForAppendTest) AppendBatch(ctx context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	started := appendStartedForAppendTest{Request: req.Clone(), release: make(chan struct{})}
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	a.startedC <- started
	select {
	case <-started.release:
	case <-ctx.Done():
		return AppendBatchResult{}, ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	items := make([]AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		msg.MessageSeq = a.nextSeq
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
		a.nextSeq++
	}
	return AppendBatchResult{Items: items}, nil
}

func (a *blockingAppenderForAppendTest) waitStarted(t *testing.T) appendStartedForAppendTest {
	t.Helper()
	select {
	case started := <-a.startedC:
		return started
	case <-time.After(time.Second):
		t.Fatalf("append did not start")
		return appendStartedForAppendTest{}
	}
}

func (a *blockingAppenderForAppendTest) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (s appendStartedForAppendTest) Release() {
	close(s.release)
}

type payloadAliasAppenderForAppendTest struct {
	payload  []byte
	sawAlias bool
}

func (a *payloadAliasAppenderForAppendTest) AppendBatch(_ context.Context, req AppendBatchRequest) (AppendBatchResult, error) {
	items := make([]AppendBatchItemResult, len(req.Messages))
	for i, msg := range req.Messages {
		if len(msg.Payload) > 0 && len(a.payload) > 0 && &msg.Payload[0] == &a.payload[0] {
			a.sawAlias = true
		}
		msg.MessageSeq = uint64(i + 1)
		items[i] = AppendBatchItemResult{MessageID: msg.MessageID, MessageSeq: msg.MessageSeq, Message: msg}
	}
	return AppendBatchResult{Items: items}, nil
}

type contextBlockingAppenderForAppendTest struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newContextBlockingAppenderForAppendTest() *contextBlockingAppenderForAppendTest {
	return &contextBlockingAppenderForAppendTest{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

func (a *contextBlockingAppenderForAppendTest) AppendBatch(ctx context.Context, _ AppendBatchRequest) (AppendBatchResult, error) {
	a.once.Do(func() {
		close(a.started)
	})
	<-ctx.Done()
	close(a.canceled)
	return AppendBatchResult{}, ctx.Err()
}

func (a *contextBlockingAppenderForAppendTest) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-a.started:
	case <-time.After(time.Second):
		t.Fatalf("append did not start")
	}
}

func (a *contextBlockingAppenderForAppendTest) wasCanceled() bool {
	select {
	case <-a.canceled:
		return true
	default:
		return false
	}
}

type appendObservationForTest struct {
	path string
	err  error
	dur  time.Duration
}

type recordingAppendObserverForTest struct {
	mu     sync.Mutex
	events []appendObservationForTest
}

func (o *recordingAppendObserverForTest) AppendFinished(path string, err error, dur time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, appendObservationForTest{path: path, err: err, dur: dur})
}

func (o *recordingAppendObserverForTest) Events() []appendObservationForTest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]appendObservationForTest(nil), o.events...)
}

type recordingSendtraceSinkForAppendTest struct {
	mu     sync.Mutex
	events []sendtrace.Event
}

func (s *recordingSendtraceSinkForAppendTest) RecordSendTrace(event sendtrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSendtraceSinkForAppendTest) snapshot() []sendtrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sendtrace.Event(nil), s.events...)
}

func submitNoWaitForAppendTest(group *Group, target AuthorityTarget, item SendBatchItem) <-chan submitResult {
	resultC := make(chan submitResult, 1)
	go func() {
		future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{item})
		resultC <- submitResult{future: future, err: err}
	}()
	return resultC
}

func localTargetForAppendTest(channelID string) AuthorityTarget {
	target := AuthorityTarget{ChannelID: ChannelID{ID: channelID, Type: 2}, LeaderNodeID: 1, Epoch: 1, LeaderEpoch: 1}
	target.ChannelKey = channelKey(target.ChannelID)
	return target
}

func differentShardTargetsForAppendTest(t *testing.T, group *Group) (AuthorityTarget, AuthorityTarget) {
	t.Helper()
	first := localTargetForAppendTest("room-0")
	firstShard := group.shardForTarget(first)
	for i := 1; i < 128; i++ {
		next := localTargetForAppendTest("room-" + string(rune('a'+i)))
		if group.shardForTarget(next) != firstShard {
			return first, next
		}
	}
	t.Fatalf("could not find channels assigned to different shards")
	return AuthorityTarget{}, AuthorityTarget{}
}

func appendSendItemForTest(uid, channelID, payload string) SendBatchItem {
	return SendBatchItem{
		Context: context.Background(),
		Command: appendCommandForTest(uid, channelID, payload),
	}
}

func appendCommandForTest(uid, channelID, payload string) SendCommand {
	return SendCommand{
		FromUID:     uid,
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: uid + "-" + payload,
		Payload:     []byte(payload),
	}
}

func requireAppendSuccess(t *testing.T, results []SendBatchItemResult, index int, messageID uint64, messageSeq uint64) {
	t.Helper()
	if len(results) <= index {
		t.Fatalf("results len = %d, want index %d", len(results), index)
	}
	if results[index].Err != nil {
		t.Fatalf("results[%d] error = %v, want nil", index, results[index].Err)
	}
	if results[index].Result.Reason != ReasonSuccess {
		t.Fatalf("results[%d] reason = %v, want success", index, results[index].Result.Reason)
	}
	if results[index].Result.MessageID != messageID || results[index].Result.MessageSeq != messageSeq {
		t.Fatalf("results[%d] id/seq = %d/%d, want %d/%d",
			index, results[index].Result.MessageID, results[index].Result.MessageSeq, messageID, messageSeq)
	}
}

func requireAppendSuccessAnyID(t *testing.T, results []SendBatchItemResult, index int) {
	t.Helper()
	if len(results) <= index {
		t.Fatalf("results len = %d, want index %d", len(results), index)
	}
	if results[index].Err != nil {
		t.Fatalf("results[%d] error = %v, want nil", index, results[index].Err)
	}
	if results[index].Result.Reason != ReasonSuccess {
		t.Fatalf("results[%d] reason = %v, want success", index, results[index].Result.Reason)
	}
	if results[index].Result.MessageID == 0 || results[index].Result.MessageSeq == 0 {
		t.Fatalf("results[%d] id/seq = %d/%d, want non-zero", index, results[index].Result.MessageID, results[index].Result.MessageSeq)
	}
}

func committedForAppendTest(t *testing.T, group *Group, channelID ChannelID) []CommittedEnvelope {
	t.Helper()
	writer := group.writerForTest(channelID)
	if writer == nil {
		return nil
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	committed := append([]CommittedEnvelope(nil), writer.state.committed...)
	for i := range committed {
		committed[i] = committed[i].Clone()
	}
	return committed
}
