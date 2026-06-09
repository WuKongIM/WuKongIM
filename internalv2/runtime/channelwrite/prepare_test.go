package channelwrite

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
)

func TestPrepareRejectsInvalidCommandsWithoutAllocation(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  SendCommand
		want Reason
	}{
		{
			name: "empty sender",
			cmd:  SendCommand{ChannelID: "room", ChannelType: 2, Payload: []byte("payload")},
			want: ReasonAuthFail,
		},
		{
			name: "empty channel",
			cmd:  SendCommand{FromUID: "u1", ChannelType: 2, Payload: []byte("payload")},
			want: ReasonInvalidRequest,
		},
		{
			name: "empty payload",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 2},
			want: ReasonInvalidRequest,
		},
		{
			name: "no persist",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "room", ChannelType: 2, Payload: []byte("payload"), NoPersist: true},
			want: ReasonUnsupported,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ids := newSequenceIDsForPrepare(100)
			group := newPreparedGroup(t, preparePortsForTest{ids: ids})

			got := group.submitAndDrainPrepare(t, sendItemForPrepare(tt.cmd))

			requireResultReason(t, got.Results, 0, tt.want)
			if len(got.Prepared) != 0 {
				t.Fatalf("prepared items = %d, want 0", len(got.Prepared))
			}
			if ids.allocatedCount() != 0 {
				t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
			}
		})
	}
}

func TestPrepareRequestScopedSendRequiresSyncOnce(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	group := newPreparedGroup(t, preparePortsForTest{ids: ids})

	got := group.submitAndDrainPrepare(t, sendItemForPrepare(SendCommand{
		FromUID:           "u1",
		Payload:           []byte("payload"),
		RequestScoped:     true,
		MessageScopedUIDs: []string{"u2"},
	}))

	if !errors.Is(got.Results[0].Err, ErrRequestSubscribersRequireSyncOnce) {
		t.Fatalf("result error = %v, want %v", got.Results[0].Err, ErrRequestSubscribersRequireSyncOnce)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared items = %d, want 0", len(got.Prepared))
	}
	if ids.allocatedCount() != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
	}
}

func TestPrepareRequestScopedSendDerivesChannel(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	clock := fixedClockForPrepare{now: time.Unix(123, 456_000_000)}
	router := newBlockingRecipientRouterForCommitTest()
	scoped, err := runtimechannelid.RequestSubscriberChannelFor([]string{"u2", "u3"})
	if err != nil {
		t.Fatalf("RequestSubscriberChannelFor() error = %v", err)
	}
	group := newPreparedGroupWithOptions(t, Options{
		LocalNodeID:                1,
		MessageID:                  ids,
		EffectWorkerCount:          1,
		Clock:                      clock,
		RecipientAuthorityResolver: staticRecipientAuthorityResolverForCommitTest{nodeID: 1},
		RecipientRouter:            router,
	})
	t.Cleanup(router.release)

	got := group.submitAndDrainPrepareToTarget(t, AuthorityTarget{
		ChannelID:    ChannelID{ID: scoped.CommandChannelID, Type: scoped.ChannelType},
		ChannelKey:   channelKey(ChannelID{ID: scoped.CommandChannelID, Type: scoped.ChannelType}),
		LeaderNodeID: 1,
	}, sendItemForPrepare(SendCommand{
		FromUID:           "u1",
		Payload:           []byte("payload"),
		SyncOnce:          true,
		RequestScoped:     true,
		MessageScopedUIDs: []string{"u2", " u3 ", "u2"},
	}))

	requireAppendSuccess(t, got.Results, 0, 100, 1)
	request := group.singleAppendRequest(t)
	if request.ChannelID.ID != scoped.CommandChannelID || request.ChannelID.Type != scoped.ChannelType {
		t.Fatalf("request-scoped channel = %q/%d, want %q/%d", request.ChannelID.ID, request.ChannelID.Type, scoped.CommandChannelID, scoped.ChannelType)
	}
	committed := committedForAppendTest(t, group.group, request.ChannelID)
	if len(committed) != 1 {
		t.Fatalf("committed events = %d, want 1", len(committed))
	}
	if gotUIDs := committed[0].MessageScopedUIDs; len(gotUIDs) != 2 || gotUIDs[0] != "u2" || gotUIDs[1] != "u3" {
		t.Fatalf("message-scoped uids = %#v, want normalized u2,u3", gotUIDs)
	}
	if request.Messages[0].MessageID != 100 {
		t.Fatalf("message id = %d, want 100", request.Messages[0].MessageID)
	}
	if request.Messages[0].ServerTimestampMS != clock.now.UnixMilli() {
		t.Fatalf("server timestamp = %d, want %d", request.Messages[0].ServerTimestampMS, clock.now.UnixMilli())
	}
}

func TestPrepareNormalizesPersonChannel(t *testing.T) {
	group := newPreparedGroup(t, preparePortsForTest{ids: newSequenceIDsForPrepare(200)})
	want := runtimechannelid.EncodePersonChannel("u1", "u2")

	got := group.submitAndDrainPrepareToTarget(t, AuthorityTarget{
		ChannelID:    ChannelID{ID: want, Type: 1},
		ChannelKey:   channelKey(ChannelID{ID: want, Type: 1}),
		LeaderNodeID: 1,
	}, sendItemForPrepare(SendCommand{
		FromUID:                "u1",
		ChannelID:              "u2",
		ChannelType:            1,
		NormalizePersonChannel: true,
		Payload:                []byte("payload"),
	}))

	requireAppendSuccess(t, got.Results, 0, 200, 1)
	request := group.singleAppendRequest(t)
	if request.ChannelID.ID != want {
		t.Fatalf("append channel id = %q, want normalized %q", request.ChannelID.ID, want)
	}
}

func TestPrepareRequestScopedTargetMismatchReturnsStaleRouteWithoutState(t *testing.T) {
	group := newPreparedGroupWithOptions(t, Options{
		LocalNodeID:  1,
		ReactorCount: 4,
		MessageID:    newSequenceIDsForPrepare(100),
	})
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "original", Type: 2},
		ChannelKey:   channelKey(ChannelID{ID: "original", Type: 2}),
		LeaderNodeID: 1,
	}

	got := group.submitAndDrainPrepareToTarget(t, target, sendItemForPrepare(SendCommand{
		FromUID:           "u1",
		Payload:           []byte("payload"),
		SyncOnce:          true,
		RequestScoped:     true,
		MessageScopedUIDs: []string{"u2", "u3"},
	}))

	if !errors.Is(got.Results[0].Err, ErrStaleRoute) {
		t.Fatalf("result error = %v, want ErrStaleRoute", got.Results[0].Err)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared state items = %d, want 0", len(got.Prepared))
	}
	if group.group.StateCountForTest() != 0 {
		t.Fatalf("state count = %d, want 0", group.group.StateCountForTest())
	}
}

func TestPrepareNormalizedPersonTargetMismatchReturnsStaleRouteWithoutState(t *testing.T) {
	group := newPreparedGroupWithOptions(t, Options{
		LocalNodeID:  1,
		ReactorCount: 4,
		MessageID:    newSequenceIDsForPrepare(100),
	})
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "u2", Type: 1},
		ChannelKey:   channelKey(ChannelID{ID: "u2", Type: 1}),
		LeaderNodeID: 1,
	}

	got := group.submitAndDrainPrepareToTarget(t, target, sendItemForPrepare(SendCommand{
		FromUID:                "u1",
		ChannelID:              "u2",
		ChannelType:            1,
		NormalizePersonChannel: true,
		Payload:                []byte("payload"),
	}))

	if !errors.Is(got.Results[0].Err, ErrStaleRoute) {
		t.Fatalf("result error = %v, want ErrStaleRoute", got.Results[0].Err)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared state items = %d, want 0", len(got.Prepared))
	}
	if group.group.StateCountForTest() != 0 {
		t.Fatalf("state count = %d, want 0", group.group.StateCountForTest())
	}
}

func TestPrepareAuthorizerDenialReturnsDecisionReason(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	authorizer := &recordingAuthorizerForPrepare{
		decision: Decision{Allowed: false, Reason: ReasonChannelNotExist},
	}
	group := newPreparedGroup(t, preparePortsForTest{ids: ids, authorizer: authorizer})

	got := group.submitAndDrainPrepare(t, sendItemForPrepare(validPrepareCommand("u1", "room", "payload")))

	requireResultReason(t, got.Results, 0, ReasonChannelNotExist)
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared items = %d, want 0", len(got.Prepared))
	}
	if ids.allocatedCount() != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
	}
	if authorizer.callCount() != 1 {
		t.Fatalf("authorizer calls = %d, want 1", authorizer.callCount())
	}
}

func TestPrepareAuthorizerDenialWithSuccessReasonFallsBackToInvalidRequest(t *testing.T) {
	group := newPreparedGroup(t, preparePortsForTest{
		ids:        newSequenceIDsForPrepare(100),
		authorizer: &recordingAuthorizerForPrepare{decision: Decision{Allowed: false, Reason: ReasonSuccess}},
	})

	got := group.submitAndDrainPrepare(t, sendItemForPrepare(validPrepareCommand("u1", "room", "payload")))

	requireResultReason(t, got.Results, 0, ReasonInvalidRequest)
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared items = %d, want 0", len(got.Prepared))
	}
}

func TestPrepareSenderFenceErrorStopsBeforeAuthorizationAndAllocation(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	fenceErr := errors.New("sender fence stale")
	authorizer := &recordingAuthorizerForPrepare{decision: Decision{Allowed: true, Reason: ReasonSuccess}}
	group := newPreparedGroup(t, preparePortsForTest{
		ids:        ids,
		authorizer: authorizer,
		fence:      senderFenceForPrepare{err: fenceErr},
	})

	got := group.submitAndDrainPrepare(t, sendItemForPrepare(validPrepareCommand("u1", "room", "payload")))

	if !errors.Is(got.Results[0].Err, fenceErr) {
		t.Fatalf("result error = %v, want %v", got.Results[0].Err, fenceErr)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared items = %d, want 0", len(got.Prepared))
	}
	if authorizer.callCount() != 0 {
		t.Fatalf("authorizer calls = %d, want 0", authorizer.callCount())
	}
	if ids.allocatedCount() != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
	}
}

func TestPrepareIdempotencyHitBypassesAllocationAndPendingAppend(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	existing := SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess}
	idempotency := &recordingIdempotencyForPrepare{result: existing, ok: true}
	group := newPreparedGroup(t, preparePortsForTest{ids: ids, idempotency: idempotency})

	got := group.submitAndDrainPrepare(t, sendItemForPrepare(SendCommand{
		FromUID:     "u1",
		ClientMsgNo: "client-1",
		ChannelID:   "room",
		ChannelType: 2,
		Payload:     []byte("payload"),
	}))

	if got.Results[0].Err != nil {
		t.Fatalf("result error = %v, want nil", got.Results[0].Err)
	}
	if got.Results[0].Result != existing {
		t.Fatalf("result = %#v, want idempotent %#v", got.Results[0].Result, existing)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared items = %d, want 0", len(got.Prepared))
	}
	if ids.allocatedCount() != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
	}
	if gotQueries := idempotency.queriesSnapshot(); len(gotQueries) != 1 {
		t.Fatalf("idempotency queries = %d, want 1", len(gotQueries))
	} else if gotQueries[0] != (IdempotencyQuery{FromUID: "u1", ClientMsgNo: "client-1", ChannelID: "room", ChannelType: 2}) {
		t.Fatalf("idempotency query = %#v, want canonical sender/client/channel", gotQueries[0])
	}
}

func TestPrepareIdempotencyUsesNormalizedPersonChannel(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	existing := SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess}
	idempotency := &recordingIdempotencyForPrepare{result: existing, ok: true}
	group := newPreparedGroup(t, preparePortsForTest{ids: ids, idempotency: idempotency})
	wantChannelID := runtimechannelid.EncodePersonChannel("u1", "u2")

	got := group.submitAndDrainPrepareToTarget(t, AuthorityTarget{
		ChannelID:    ChannelID{ID: wantChannelID, Type: 1},
		ChannelKey:   channelKey(ChannelID{ID: wantChannelID, Type: 1}),
		LeaderNodeID: 1,
	}, sendItemForPrepare(SendCommand{
		FromUID:                "u1",
		ClientMsgNo:            "client-1",
		ChannelID:              "u2",
		ChannelType:            1,
		NormalizePersonChannel: true,
		Payload:                []byte("payload"),
	}))

	if got.Results[0].Err != nil {
		t.Fatalf("result error = %v, want nil", got.Results[0].Err)
	}
	if got.Results[0].Result != existing {
		t.Fatalf("result = %#v, want idempotent %#v", got.Results[0].Result, existing)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared items = %d, want 0", len(got.Prepared))
	}
	if ids.allocatedCount() != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
	}
	if gotQueries := idempotency.queriesSnapshot(); len(gotQueries) != 1 {
		t.Fatalf("idempotency queries = %d, want 1", len(gotQueries))
	} else if gotQueries[0].ChannelID != wantChannelID || gotQueries[0].ChannelType != 1 {
		t.Fatalf("idempotency query = %#v, want normalized channel %q/1", gotQueries[0], wantChannelID)
	}
}

func TestPrepareIdempotencyNormalizedPersonTargetMismatchReturnsStaleRoute(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	existing := SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess}
	idempotency := &recordingIdempotencyForPrepare{result: existing, ok: true}
	group := newPreparedGroupWithOptions(t, Options{
		LocalNodeID:  1,
		ReactorCount: 4,
		MessageID:    ids,
		Idempotency:  idempotency,
	})
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "u2", Type: 1},
		ChannelKey:   channelKey(ChannelID{ID: "u2", Type: 1}),
		LeaderNodeID: 1,
	}

	got := group.submitAndDrainPrepareToTarget(t, target, sendItemForPrepare(SendCommand{
		FromUID:                "u1",
		ClientMsgNo:            "client-1",
		ChannelID:              "u2",
		ChannelType:            1,
		NormalizePersonChannel: true,
		Payload:                []byte("payload"),
	}))

	if !errors.Is(got.Results[0].Err, ErrStaleRoute) {
		t.Fatalf("result error = %v, want ErrStaleRoute", got.Results[0].Err)
	}
	if got.Results[0].Result != (SendResult{}) {
		t.Fatalf("result = %#v, want zero result after stale route", got.Results[0].Result)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared state items = %d, want 0", len(got.Prepared))
	}
	if group.group.StateCountForTest() != 0 {
		t.Fatalf("state count = %d, want 0", group.group.StateCountForTest())
	}
	if ids.allocatedCount() != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
	}
	wantChannelID := runtimechannelid.EncodePersonChannel("u1", "u2")
	if gotQueries := idempotency.queriesSnapshot(); len(gotQueries) != 1 {
		t.Fatalf("idempotency queries = %d, want 1", len(gotQueries))
	} else if gotQueries[0].ChannelID != wantChannelID || gotQueries[0].ChannelType != 1 {
		t.Fatalf("idempotency query = %#v, want normalized channel %q/1", gotQueries[0], wantChannelID)
	}
}

func TestPrepareIdempotencyRequestScopedTargetMismatchReturnsStaleRoute(t *testing.T) {
	ids := newSequenceIDsForPrepare(100)
	existing := SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess}
	idempotency := &recordingIdempotencyForPrepare{result: existing, ok: true}
	group := newPreparedGroupWithOptions(t, Options{
		LocalNodeID:  1,
		ReactorCount: 4,
		MessageID:    ids,
		Idempotency:  idempotency,
	})
	target := AuthorityTarget{
		ChannelID:    ChannelID{ID: "original", Type: 2},
		ChannelKey:   channelKey(ChannelID{ID: "original", Type: 2}),
		LeaderNodeID: 1,
	}

	got := group.submitAndDrainPrepareToTarget(t, target, sendItemForPrepare(SendCommand{
		FromUID:           "u1",
		ClientMsgNo:       "client-1",
		Payload:           []byte("payload"),
		SyncOnce:          true,
		RequestScoped:     true,
		MessageScopedUIDs: []string{"u2", "u3"},
	}))

	if !errors.Is(got.Results[0].Err, ErrStaleRoute) {
		t.Fatalf("result error = %v, want ErrStaleRoute", got.Results[0].Err)
	}
	if got.Results[0].Result != (SendResult{}) {
		t.Fatalf("result = %#v, want zero result after stale route", got.Results[0].Result)
	}
	if len(got.Prepared) != 0 {
		t.Fatalf("prepared state items = %d, want 0", len(got.Prepared))
	}
	if group.group.StateCountForTest() != 0 {
		t.Fatalf("state count = %d, want 0", group.group.StateCountForTest())
	}
	if ids.allocatedCount() != 0 {
		t.Fatalf("allocated ids = %d, want 0", ids.allocatedCount())
	}
	scoped, err := runtimechannelid.RequestSubscriberChannelFor([]string{"u2", "u3"})
	if err != nil {
		t.Fatalf("RequestSubscriberChannelFor() error = %v", err)
	}
	if gotQueries := idempotency.queriesSnapshot(); len(gotQueries) != 1 {
		t.Fatalf("idempotency queries = %d, want 1", len(gotQueries))
	} else if gotQueries[0].ChannelID != scoped.CommandChannelID || gotQueries[0].ChannelType != scoped.ChannelType {
		t.Fatalf("idempotency query = %#v, want request-scoped channel %q/%d", gotQueries[0], scoped.CommandChannelID, scoped.ChannelType)
	}
}

func TestPrepareCanceledItemsDoNotBlockOtherItems(t *testing.T) {
	ids := newSequenceIDsForPrepare(300)
	group := newPreparedGroup(t, preparePortsForTest{ids: ids})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	got := group.submitAndDrainPrepare(t,
		sendItemForPrepare(validPrepareCommand("u1", "room", "first")),
		SendBatchItem{Context: canceled, Command: validPrepareCommand("u1", "room", "canceled")},
		sendItemForPrepare(validPrepareCommand("u1", "room", "third")),
	)

	requireAppendSuccess(t, got.Results, 0, 300, 1)
	if !errors.Is(got.Results[1].Err, context.Canceled) {
		t.Fatalf("canceled result error = %v, want context.Canceled", got.Results[1].Err)
	}
	requireAppendSuccess(t, got.Results, 2, 301, 2)
	request := group.singleAppendRequest(t)
	if len(request.Messages) != 2 {
		t.Fatalf("appended messages = %d, want 2", len(request.Messages))
	}
	if payload := string(request.Messages[0].Payload); payload != "first" {
		t.Fatalf("first append payload = %q, want first", payload)
	}
	if payload := string(request.Messages[1].Payload); payload != "third" {
		t.Fatalf("second append payload = %q, want third", payload)
	}
	if ids.allocatedCount() != 2 {
		t.Fatalf("allocated ids = %d, want 2", ids.allocatedCount())
	}
}

func TestPrepareValidItemsAppendInInputOrder(t *testing.T) {
	ids := newSequenceIDsForPrepare(400)
	clock := fixedClockForPrepare{now: time.Unix(500, 0)}
	group := newPreparedGroup(t, preparePortsForTest{ids: ids, clock: clock})

	got := group.submitAndDrainPrepare(t,
		sendItemForPrepare(validPrepareCommand("u1", "room", "one")),
		sendItemForPrepare(validPrepareCommand("u1", "room", "two")),
		sendItemForPrepare(validPrepareCommand("u1", "room", "three")),
	)

	requireAppendSuccess(t, got.Results, 0, 400, 1)
	requireAppendSuccess(t, got.Results, 1, 401, 2)
	requireAppendSuccess(t, got.Results, 2, 402, 3)
	request := group.singleAppendRequest(t)
	if len(request.Messages) != 3 {
		t.Fatalf("appended messages = %d, want 3", len(request.Messages))
	}
	for i, want := range []struct {
		payload   string
		messageID uint64
	}{
		{payload: "one", messageID: 400},
		{payload: "two", messageID: 401},
		{payload: "three", messageID: 402},
	} {
		if gotPayload := string(request.Messages[i].Payload); gotPayload != want.payload {
			t.Fatalf("append[%d] payload = %q, want %q", i, gotPayload, want.payload)
		}
		if request.Messages[i].MessageID != want.messageID {
			t.Fatalf("append[%d] message id = %d, want %d", i, request.Messages[i].MessageID, want.messageID)
		}
		if request.Messages[i].ServerTimestampMS != clock.now.UnixMilli() {
			t.Fatalf("append[%d] server timestamp = %d, want %d", i, request.Messages[i].ServerTimestampMS, clock.now.UnixMilli())
		}
	}
}

type preparePortsForTest struct {
	ids         MessageIDAllocator
	authorizer  Authorizer
	idempotency IdempotencyStore
	fence       SenderFenceValidator
	clock       Clock
}

type preparedGroupForTest struct {
	group    *Group
	appender *recordingAppenderForAppendTest
}

type prepareDrainForTest struct {
	Results  []SendBatchItemResult
	Prepared []preparedSend
}

func newPreparedGroup(t *testing.T, ports preparePortsForTest) *preparedGroupForTest {
	t.Helper()

	opts := Options{
		LocalNodeID:       1,
		MessageID:         ports.ids,
		Authorizer:        ports.authorizer,
		Idempotency:       ports.idempotency,
		SenderFence:       ports.fence,
		EffectWorkerCount: 1,
		Clock:             ports.clock,
	}
	return newPreparedGroupWithOptions(t, opts)
}

func newPreparedGroupWithOptions(t *testing.T, opts Options) *preparedGroupForTest {
	t.Helper()

	var appender *recordingAppenderForAppendTest
	if opts.Appender == nil {
		appender = newRecordingAppenderForAppendTest()
		opts.Appender = appender
	} else {
		appender, _ = opts.Appender.(*recordingAppenderForAppendTest)
	}
	group := newStartedTestGroup(t, opts)
	return &preparedGroupForTest{group: group, appender: appender}
}

func (g *preparedGroupForTest) submitAndDrainPrepare(t *testing.T, items ...SendBatchItem) prepareDrainForTest {
	t.Helper()

	return g.submitAndDrainPrepareToTarget(t, targetForPrepareItems(items), items...)
}

func (g *preparedGroupForTest) submitAndDrainPrepareToTarget(t *testing.T, target AuthorityTarget, items ...SendBatchItem) prepareDrainForTest {
	t.Helper()

	future, err := g.group.SubmitLocal(context.Background(), target, items)
	if err != nil {
		t.Fatalf("SubmitLocal() error = %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results, err := future.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Future.Wait() error = %v", err)
	}
	return prepareDrainForTest{
		Results:  results,
		Prepared: g.group.preparedForTest(),
	}
}

func targetForPrepareItems(items []SendBatchItem) AuthorityTarget {
	target := AuthorityTarget{LeaderNodeID: 1, Epoch: 1, LeaderEpoch: 1}
	if len(items) == 0 {
		return target
	}
	cmd := items[0].Command
	target.ChannelID = ChannelID{ID: cmd.ChannelID, Type: cmd.ChannelType}
	if cmd.ChannelID != "" && cmd.ChannelType != 0 {
		target.ChannelKey = channelKey(target.ChannelID)
	}
	return target
}

func (g *preparedGroupForTest) pendingForChannel(t *testing.T, channelID ChannelID) []preparedSend {
	t.Helper()

	key := channelKey(channelID)
	for _, reactor := range g.group.reactors {
		reactor.mu.Lock()
		state := reactor.states[key]
		if state != nil {
			pending := append([]preparedSend(nil), state.pendingItems...)
			reactor.mu.Unlock()
			return pending
		}
		reactor.mu.Unlock()
	}
	return nil
}

func (g *preparedGroupForTest) singleAppendRequest(t *testing.T) AppendBatchRequest {
	t.Helper()
	if g.appender == nil {
		t.Fatalf("prepared group appender is not recording")
	}
	requests := g.appender.Requests()
	if len(requests) != 1 {
		t.Fatalf("append requests = %d, want 1", len(requests))
	}
	return requests[0]
}

func (g *Group) preparedForTest() []preparedSend {
	var out []preparedSend
	for _, reactor := range g.reactors {
		reactor.mu.Lock()
		for _, state := range reactor.states {
			out = append(out, state.pendingItems...)
		}
		reactor.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Index < out[j].Index
	})
	return out
}

func sendItemForPrepare(cmd SendCommand) SendBatchItem {
	return SendBatchItem{Context: context.Background(), Command: cmd}
}

func validPrepareCommand(uid, channelID, payload string) SendCommand {
	return SendCommand{
		FromUID:     uid,
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: uid + "-" + payload,
		Payload:     []byte(payload),
	}
}

func requireResultReason(t *testing.T, results []SendBatchItemResult, index int, reason Reason) {
	t.Helper()
	if len(results) <= index {
		t.Fatalf("results len = %d, want index %d", len(results), index)
	}
	if results[index].Err != nil {
		t.Fatalf("results[%d] error = %v, want nil", index, results[index].Err)
	}
	if results[index].Result.Reason != reason {
		t.Fatalf("results[%d] reason = %v, want %v", index, results[index].Result.Reason, reason)
	}
}

type sequenceIDsForPrepare struct {
	mu        sync.Mutex
	next      uint64
	allocated int
}

func newSequenceIDsForPrepare(next uint64) *sequenceIDsForPrepare {
	return &sequenceIDsForPrepare{next: next}
}

func (s *sequenceIDsForPrepare) Next() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	s.next++
	s.allocated++
	return id
}

func (s *sequenceIDsForPrepare) allocatedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allocated
}

type fixedClockForPrepare struct {
	now time.Time
}

func (c fixedClockForPrepare) Now() time.Time {
	return c.now
}

type recordingAuthorizerForPrepare struct {
	mu       sync.Mutex
	decision Decision
	err      error
	calls    int
	commands []SendCommand
}

func (a *recordingAuthorizerForPrepare) AuthorizeSend(_ context.Context, cmd SendCommand) (Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	a.commands = append(a.commands, cmd.Clone())
	if a.err != nil {
		return Decision{}, a.err
	}
	return a.decision, nil
}

func (a *recordingAuthorizerForPrepare) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

type recordingIdempotencyForPrepare struct {
	mu      sync.Mutex
	result  SendResult
	ok      bool
	err     error
	queries []IdempotencyQuery
}

func (i *recordingIdempotencyForPrepare) LookupSend(_ context.Context, query IdempotencyQuery) (SendResult, bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.queries = append(i.queries, query)
	return i.result, i.ok, i.err
}

func (i *recordingIdempotencyForPrepare) queriesSnapshot() []IdempotencyQuery {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]IdempotencyQuery(nil), i.queries...)
}

type senderFenceForPrepare struct {
	err error
}

func (f senderFenceForPrepare) ValidateSender(context.Context, SendCommand) error {
	return f.err
}
