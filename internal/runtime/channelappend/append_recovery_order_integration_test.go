//go:build integration

package channelappend

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestGroupRecoveryDoesNotReplayAfterUnprovedPredecessor(t *testing.T) {
	for _, routeErr := range []error{ErrNotLeader, ErrStaleRoute, ErrRouteNotReady, ErrNotChannelAuthority, context.Canceled} {
		t.Run(routeErr.Error(), func(t *testing.T) {
			appender := newRecordingAppenderForAppendTest()
			appender.nextSeq = 8
			appender.errs = []error{fmt.Errorf("%w: original proposal outcome unproven", ErrAppendFailed), nil}
			// A is a local index candidate whose current-Leader committed proof
			// failed. A fresh index miss alone does not perform that routed read.
			// B has no local index candidate; C has a proved committed receipt.
			idempotency := &sequencedIdempotencyForAppendTest{
				results: []idempotencyResultForAppendTest{
					{err: routeErr},
					{ok: false},
					{result: SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess}, ok: true},
				},
			}
			group := newStartedTestGroup(t, Options{
				LocalNodeID: 1,
				MessageID:   newSequenceIDsForPrepare(300),
				Appender:    appender,
				Idempotency: idempotency,
			})
			target := localTargetForAppendTest("recovery-order")
			items := []SendBatchItem{
				appendSendItemForTest("sender", target.ChannelID.ID, "unproved-predecessor"),
				appendSendItemForTest("sender", target.ChannelID.ID, "fresh-successor"),
				appendSendItemForTest("sender", target.ChannelID.ID, "committed-sibling"),
			}
			future, err := group.SubmitLocal(context.Background(), target, items)
			if err != nil {
				t.Fatal(err)
			}
			results := waitFutureForTest(t, future)
			if calls := appender.Calls(); calls != 1 {
				t.Fatalf("storage append calls = %d, want original attempt only while predecessor proof is unresolved", calls)
			}
			if len(results) != 3 {
				t.Fatalf("result cardinality = %d, want 3", len(results))
			}
			for index := 0; index < 2; index++ {
				if !errors.Is(results[index].Err, routeErr) {
					t.Fatalf("result %d = %#v, want preserved retryable proof failure", index, results[index])
				}
				if errors.Is(results[index].Err, ErrAppendNotSubmitted) {
					t.Fatalf("result %d promoted uncertain original proposal to not-submitted", index)
				}
			}
			requireAppendSuccess(t, results, 2, 42, 7)

			// Once the caller has refreshed its authority, only unresolved items
			// reenter the public seam. Their durable order must still be A then B.
			retry, err := group.SubmitLocal(context.Background(), target, items[:2])
			if err != nil {
				t.Fatal(err)
			}
			retried := waitFutureForTest(t, retry)
			if len(retried) != 2 || retried[0].Err != nil || retried[1].Err != nil ||
				retried[0].Result.MessageSeq != 8 || retried[1].Result.MessageSeq != 9 {
				t.Fatalf("resolved retry = %#v, want successful predecessor sequence 8 then successor 9", retried)
			}
			requests := appender.Requests()
			if len(requests) != 2 || len(requests[1].Messages) != 2 ||
				string(requests[1].Messages[0].Payload) != "unproved-predecessor" ||
				string(requests[1].Messages[1].Payload) != "fresh-successor" {
				t.Fatalf("resolved storage retry did not retain the original two-item order")
			}
			t.Logf("recovery order receipt: original_appends=1 deferred_items=2 retained_committed=1 final_appends=2 final_sequences=8,9")
		})
	}
}

// A resolved prefix remains independently safe to replay; committed receipts
// beyond an unresolved predecessor must still be inspected and preserved.
func TestGroupRecoveryReplaysOnlyMissesBeforeUnprovedPredecessor(t *testing.T) {
	appender := newRecordingAppenderForAppendTest()
	appender.nextSeq = 8
	appender.errs = []error{ErrAppendFailed, nil}
	observer := &recordingAppendObserverForTest{}
	idempotency := &sequencedIdempotencyForAppendTest{results: []idempotencyResultForAppendTest{
		{ok: false},
		{err: ErrNotLeader},
		{ok: false},
		{result: SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess}, ok: true},
	}}
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(300), Appender: appender,
		Idempotency: idempotency, Observer: observer,
	})
	target := localTargetForAppendTest("recovery-prefix")
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("sender", target.ChannelID.ID, "safe-prefix"),
		appendSendItemForTest("sender", target.ChannelID.ID, "unproved-predecessor"),
		appendSendItemForTest("sender", target.ChannelID.ID, "fresh-successor"),
		appendSendItemForTest("sender", target.ChannelID.ID, "committed-sibling"),
	})
	if err != nil {
		t.Fatal(err)
	}
	results := waitFutureForTest(t, future)
	requests := appender.Requests()
	if len(requests) != 2 || len(requests[1].Messages) != 1 ||
		string(requests[1].Messages[0].Payload) != "safe-prefix" {
		t.Fatalf("storage recovery = %#v, want one bounded safe-prefix replay", requests)
	}
	requireAppendSuccess(t, results, 0, 300, 8)
	requireAppendSuccess(t, results, 3, 42, 7)
	for _, index := range []int{1, 2} {
		if !errors.Is(results[index].Err, ErrNotLeader) || errors.Is(results[index].Err, ErrAppendNotSubmitted) {
			t.Fatalf("result %d = %#v, want unresolved retryable proof", index, results[index])
		}
	}
	want := IdempotencyRecoveryObservation{RecoveredItems: 1, UnresolvedItems: 1, LookupErrorItems: 1}
	if got := observer.IdempotencyRecoveryEvents(); len(got) != 1 || got[0] != want {
		t.Fatalf("recovery observation = %#v, want %#v", got, want)
	}
}

func TestGroupRecoveryDefersUnprovedSuffixWithoutCommittedSibling(t *testing.T) {
	appender := newRecordingAppenderForAppendTest()
	appender.err = ErrAppendFailed
	idempotency := &sequencedIdempotencyForAppendTest{results: []idempotencyResultForAppendTest{
		{err: ErrStaleRoute}, {ok: false},
	}}
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(300),
		Appender: appender, Idempotency: idempotency,
	})
	target := localTargetForAppendTest("unproved-recovery")
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		appendSendItemForTest("sender", target.ChannelID.ID, "unproved-predecessor"),
		appendSendItemForTest("sender", target.ChannelID.ID, "fresh-successor"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range waitFutureForTest(t, future) {
		if !errors.Is(result.Err, ErrStaleRoute) || errors.Is(result.Err, ErrAppendNotSubmitted) {
			t.Fatalf("result %d = %#v, want unresolved retryable proof", index, result)
		}
	}
	if got := appender.Calls(); got != 1 {
		t.Fatalf("storage append calls = %d, want no replay without positive proof", got)
	}
}

func TestGroupRecoveryDoesNotFenceBehindCanceledCaller(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	appender := newRecordingAppenderForAppendTest()
	appender.nextSeq = 8
	appender.errs = []error{ErrAppendFailed, nil}
	store := &sequencedIdempotencyForAppendTest{results: []idempotencyResultForAppendTest{
		{err: context.Canceled},
		{ok: false},
		{result: SendResult{MessageID: 42, MessageSeq: 7, Reason: ReasonSuccess}, ok: true},
	}}
	idempotency := recoveryLookupFunc(func(ctx context.Context, query IdempotencyQuery) (SendResult, bool, error) {
		cancel()
		return store.LookupSend(ctx, query)
	})
	group := newStartedTestGroup(t, Options{
		LocalNodeID: 1, MessageID: newSequenceIDsForPrepare(300),
		Appender: appender, Idempotency: idempotency,
	})
	target := localTargetForAppendTest("terminal-predecessor")
	first := appendSendItemForTest("sender", target.ChannelID.ID, "canceled-predecessor")
	first.Context = caller
	future, err := group.SubmitLocal(context.Background(), target, []SendBatchItem{
		first,
		appendSendItemForTest("sender", target.ChannelID.ID, "fresh-successor"),
		appendSendItemForTest("sender", target.ChannelID.ID, "committed-sibling"),
	})
	if err != nil {
		t.Fatal(err)
	}
	results := waitFutureForTest(t, future)
	if !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("canceled caller result = %#v", results[0])
	}
	requireAppendSuccess(t, results, 1, 301, 8)
	requireAppendSuccess(t, results, 2, 42, 7)
	if got := appender.Calls(); got != 2 {
		t.Fatalf("storage append calls = %d, want original attempt and live successor replay", got)
	}
}

type recoveryLookupFunc func(context.Context, IdempotencyQuery) (SendResult, bool, error)

func (lookup recoveryLookupFunc) LookupSend(ctx context.Context, query IdempotencyQuery) (SendResult, bool, error) {
	return lookup(ctx, query)
}
