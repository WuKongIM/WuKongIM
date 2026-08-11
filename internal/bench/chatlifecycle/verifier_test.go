package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestVerifierSendackAndTerminalCompletion(t *testing.T) {
	model, verifier := newTestVerifier(t, 256, 256, 64, 10*time.Second)
	logical := mustLogicalSend(t, model, 1, 42, TrafficPerson, "sender-private", "recipient-private")
	registeredAt := time.Unix(100, 0)
	if err := verifier.RegisterSend(logical, registeredAt); err != nil {
		t.Fatalf("RegisterSend() error = %v", err)
	}
	policy := newTestRetryPolicy(t, model)
	for number := uint8(0); number <= 3; number++ {
		attempt, err := policy.Attempt(logical, number)
		if err != nil {
			t.Fatalf("Attempt(%d) error = %v", number, err)
		}
		if err := verifier.ObserveAttempt(logical, attempt, uint64(number)+1); err != nil {
			t.Fatalf("ObserveAttempt(%d) error = %v", number, err)
		}
	}
	ack := &frame.SendackPacket{
		ClientSeq:   4,
		MessageID:   9001,
		MessageSeq:  77,
		ClientMsgNo: logical.ClientMsgNo,
		ReasonCode:  frame.ReasonSuccess,
	}
	if err := verifier.HandleSendack(ack); err != nil {
		t.Fatalf("HandleSendack() error = %v", err)
	}
	snapshot := verifier.Snapshot()
	if snapshot.Sent != 1 || snapshot.Attempts != 4 || snapshot.RetryAttempts != 3 || snapshot.Acknowledged != 1 || snapshot.Terminal != 0 {
		t.Fatalf("send counters = %+v", snapshot)
	}
	if snapshot.PendingCurrent != 1 || snapshot.PendingUnfinished != 0 {
		t.Fatalf("pending counters = %+v", snapshot)
	}
	if err := verifier.ReleaseSend(logical); err != nil {
		t.Fatalf("ReleaseSend() error = %v", err)
	}
	if got := verifier.Snapshot().PendingCurrent; got != 0 {
		t.Fatalf("pending after release = %d, want 0", got)
	}

	terminal := mustLogicalSend(t, model, 1, 43, TrafficGroup, "sender-private", "group-private")
	if err := verifier.RegisterSend(terminal, registeredAt); err != nil {
		t.Fatalf("RegisterSend(terminal) error = %v", err)
	}
	assertVerificationCode(t, verifier.CompleteTerminal(terminal, TerminalSendRetryExhausted), FailureCodeTerminalSend)
	snapshot = verifier.Snapshot()
	if snapshot.Terminal != 1 || snapshot.PendingUnfinished != 0 {
		t.Fatalf("terminal counters = %+v", snapshot)
	}
	if snapshot.Classification != SyncClassificationProductFailure {
		t.Fatalf("classification = %q, want product_failure", snapshot.Classification)
	}
}

func TestVerifierCountsFirstAttemptFailuresOncePerLogicalSend(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Minute)
	policy := newTestRetryPolicy(t, model)

	rejected := mustLogicalSend(t, model, 0, 51, TrafficPerson, "sender", "recipient")
	if err := verifier.RegisterSend(rejected, time.Unix(100, 0)); err != nil {
		t.Fatalf("RegisterSend(rejected): %v", err)
	}
	first, err := policy.Attempt(rejected, 0)
	if err != nil {
		t.Fatalf("Attempt(rejected, 0): %v", err)
	}
	if err := verifier.ObserveAttempt(rejected, first, 101); err != nil {
		t.Fatalf("ObserveAttempt(rejected, 0): %v", err)
	}
	rejection := &frame.SendackPacket{ClientSeq: 101, ClientMsgNo: rejected.ClientMsgNo, ReasonCode: frame.ReasonSystemError}
	for duplicate := 0; duplicate < 2; duplicate++ {
		var rejectedErr *SendackRejectedError
		if err := verifier.HandleSendack(rejection); !errors.As(err, &rejectedErr) {
			t.Fatalf("HandleSendack(rejected %d) = %v, want SendackRejectedError", duplicate, err)
		}
	}
	retry, err := policy.Attempt(rejected, 1)
	if err != nil {
		t.Fatalf("Attempt(rejected, 1): %v", err)
	}
	if err := verifier.ObserveAttempt(rejected, retry, 102); err != nil {
		t.Fatalf("ObserveAttempt(rejected, 1): %v", err)
	}
	if err := verifier.ResolveAttemptError(rejected.ClientMsgNo, 102); err != nil {
		t.Fatalf("ResolveAttemptError(retry): %v", err)
	}

	transportFailed := mustLogicalSend(t, model, 0, 52, TrafficPerson, "sender", "recipient")
	if err := verifier.RegisterSend(transportFailed, time.Unix(101, 0)); err != nil {
		t.Fatalf("RegisterSend(transport): %v", err)
	}
	first, err = policy.Attempt(transportFailed, 0)
	if err != nil {
		t.Fatalf("Attempt(transport, 0): %v", err)
	}
	if err := verifier.ObserveAttempt(transportFailed, first, 201); err != nil {
		t.Fatalf("ObserveAttempt(transport, 0): %v", err)
	}
	if err := verifier.ResolveAttemptLocalAdmissionError(transportFailed.ClientMsgNo, 201); err != nil {
		t.Fatalf("ResolveAttemptLocalAdmissionError(first): %v", err)
	}
	if err := verifier.ResolveAttemptLocalAdmissionError(transportFailed.ClientMsgNo, 201); err != nil {
		t.Fatalf("ResolveAttemptLocalAdmissionError(first duplicate): %v", err)
	}

	succeeded := mustLogicalSend(t, model, 0, 53, TrafficPerson, "sender", "recipient")
	if err := verifier.RegisterSend(succeeded, time.Unix(102, 0)); err != nil {
		t.Fatalf("RegisterSend(success): %v", err)
	}
	first, err = policy.Attempt(succeeded, 0)
	if err != nil {
		t.Fatalf("Attempt(success, 0): %v", err)
	}
	if err := verifier.ObserveAttempt(succeeded, first, 301); err != nil {
		t.Fatalf("ObserveAttempt(success, 0): %v", err)
	}
	if err := verifier.HandleSendack(&frame.SendackPacket{
		ClientSeq: 301, ClientMsgNo: succeeded.ClientMsgNo, MessageID: 1, MessageSeq: 1, ReasonCode: frame.ReasonSuccess,
	}); err != nil {
		t.Fatalf("HandleSendack(success): %v", err)
	}

	snapshot := verifier.Snapshot()
	if snapshot.FirstAttempts != 3 || snapshot.FirstAttemptFailures != 2 || snapshot.FirstAttemptLocalAdmissionFailures != 1 {
		t.Fatalf("first-attempt counters = %d/%d local=%d, want 3/2 local=1; snapshot=%+v", snapshot.FirstAttempts, snapshot.FirstAttemptFailures, snapshot.FirstAttemptLocalAdmissionFailures, snapshot)
	}
}

func TestVerifierRecordsExplicitClockSendackAndRecvackLatency(t *testing.T) {
	t.Parallel()
	model, verifier := newTestVerifier(t, 16, 16, 16, 10*time.Second)
	logical := mustLogicalSend(t, model, 0, 91, TrafficPerson, "sender", "recipient")
	started := time.Unix(8_000, 0)
	if err := verifier.RegisterSend(logical, started); err != nil {
		t.Fatalf("RegisterSend: %v", err)
	}
	ack := &frame.SendackPacket{
		ClientMsgNo: logical.ClientMsgNo, MessageID: 81, MessageSeq: 82, ReasonCode: frame.ReasonSuccess,
	}
	if err := verifier.HandleSendackAt(ack, started.Add(2*time.Second)); err != nil {
		t.Fatalf("HandleSendackAt: %v", err)
	}
	if histogram := verifier.Snapshot().SendackLatency; histogram.Count != 1 || histogram.SumNanos != uint64(2*time.Second) || histogram.MaxNanos != uint64(2*time.Second) || histogram.Buckets[11] != 1 {
		t.Fatalf("sendack latency = %+v", histogram)
	}

	recv := mustRecvPacket(t, model, logical, 81, 82)
	clock := &sessionFakeClock{now: started.Add(3 * time.Second)}
	acker := advancingRecvAcker{clock: clock, by: 50 * time.Millisecond}
	if err := verifier.HandleRecvAt(context.Background(), logical.Target, recv, acker, clock.Now); err != nil {
		t.Fatalf("HandleRecvAt: %v", err)
	}
	if histogram := verifier.Snapshot().RecvackLatency; histogram.Count != 1 || histogram.SumNanos != uint64(50*time.Millisecond) || histogram.MaxNanos != uint64(50*time.Millisecond) || histogram.Buckets[6] != 1 {
		t.Fatalf("recvack latency = %+v", histogram)
	}
}

type advancingRecvAcker struct {
	clock *sessionFakeClock
	by    time.Duration
}

func (a advancingRecvAcker) AckRecv(context.Context, *frame.RecvackPacket) error {
	a.clock.Set(a.clock.Now().Add(a.by))
	return nil
}

func TestVerifierReleasedLogicalSendConsumesRegisteredSiblingAttempts(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Minute)
	logical := mustLogicalSend(t, model, 0, 44, TrafficGroup, "sender", "group")
	registeredAt := time.Unix(100, 0)
	if err := verifier.RegisterSend(logical, registeredAt); err != nil {
		t.Fatalf("RegisterSend: %v", err)
	}
	policy := newTestRetryPolicy(t, model)
	for attemptNumber, clientSeq := range []uint64{101, 102} {
		attempt, err := policy.Attempt(logical, uint8(attemptNumber))
		if err != nil {
			t.Fatalf("Attempt(%d): %v", attemptNumber, err)
		}
		if err := verifier.ObserveAttempt(logical, attempt, clientSeq); err != nil {
			t.Fatalf("ObserveAttempt(%d, %d): %v", attemptNumber, clientSeq, err)
		}
	}
	first := &frame.SendackPacket{
		ClientSeq: 101, ClientMsgNo: logical.ClientMsgNo,
		MessageID: 501, MessageSeq: 601, ReasonCode: frame.ReasonSuccess,
	}
	if err := verifier.HandleSendack(first); err != nil {
		t.Fatalf("HandleSendack(first): %v", err)
	}
	if err := verifier.ReleaseSend(logical); err != nil {
		t.Fatalf("ReleaseSend: %v", err)
	}
	if snapshot := verifier.Snapshot(); snapshot.ReleasedAttemptCurrent != 1 {
		t.Fatalf("released attempt current = %d, want 1", snapshot.ReleasedAttemptCurrent)
	}
	second := &frame.SendackPacket{
		ClientSeq: 102, ClientMsgNo: logical.ClientMsgNo,
		MessageID: 501, MessageSeq: 601, ReasonCode: frame.ReasonSuccess,
	}
	if err := verifier.HandleSendack(second); err != nil {
		t.Fatalf("HandleSendack(released sibling): %v", err)
	}
	snapshot := verifier.Snapshot()
	if snapshot.ReleasedAttemptCurrent != 0 || snapshot.UnknownSendacks != 0 || snapshot.DuplicateCompletions != 0 || snapshot.ConflictingCompletions != 0 {
		t.Fatalf("released sibling snapshot = %+v", snapshot)
	}
	unknown := *second
	unknown.ClientSeq = 999
	assertVerificationCode(t, verifier.HandleSendack(&unknown), FailureCodeUnknownSendack)
}

func TestVerifierRejectsUnknownDuplicateAndConflictingSendacks(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	logical := mustLogicalSend(t, model, 0, 7, TrafficPerson, "sender-secret", "target-secret")
	if err := verifier.RegisterSend(logical, time.Unix(1, 0)); err != nil {
		t.Fatalf("RegisterSend() error = %v", err)
	}
	ack := &frame.SendackPacket{MessageID: 41, MessageSeq: 4, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
	if err := verifier.HandleSendack(ack); err != nil {
		t.Fatalf("HandleSendack() error = %v", err)
	}
	assertVerificationCode(t, verifier.HandleSendack(ack), FailureCodeDuplicateCompletion)
	conflict := *ack
	conflict.MessageSeq++
	assertVerificationCode(t, verifier.HandleSendack(&conflict), FailureCodeConflictingCompletion)
	unknownIdentity := "private-unknown-client-message-number"
	unknown := &frame.SendackPacket{MessageID: 9, MessageSeq: 9, ClientMsgNo: unknownIdentity, ReasonCode: frame.ReasonSuccess}
	err := verifier.HandleSendack(unknown)
	assertVerificationCode(t, err, FailureCodeUnknownSendack)
	for _, secret := range []string{logical.ClientMsgNo, logical.Sender, logical.Target, unknownIdentity} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks %q: %q", secret, err)
		}
	}

	snapshot := verifier.Snapshot()
	if snapshot.DuplicateCompletions != 1 || snapshot.ConflictingCompletions != 1 || snapshot.UnknownSendacks != 1 {
		t.Fatalf("conflict counters = %+v", snapshot)
	}
	if snapshot.Classification != SyncClassificationProductFailure {
		t.Fatalf("classification = %q, want product_failure", snapshot.Classification)
	}
}

func TestVerifierRequiresValidSuccessfulSendackAndStableAttemptIdentity(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	logical := mustLogicalSend(t, model, 2, 8, TrafficPerson, "sender", "target")
	if err := verifier.RegisterSend(logical, time.Unix(1, 0)); err != nil {
		t.Fatalf("RegisterSend() error = %v", err)
	}
	tampered := RetryAttempt{Attempt: 1, ClientMsgNo: logical.ClientMsgNo + "-new"}
	assertVerificationCode(t, verifier.ObserveAttempt(logical, tampered, 1), FailureCodeGeneratorInvariant)

	badAcks := []*frame.SendackPacket{
		nil,
		{MessageID: 0, MessageSeq: 1, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess},
		{MessageID: 1, MessageSeq: 0, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess},
	}
	for _, ack := range badAcks {
		assertVerificationCode(t, verifier.HandleSendack(ack), FailureCodeInvalidSendack)
	}
	// Rejected SENDACKs may legitimately omit server message identity. Only a
	// successful completion requires positive MessageID and MessageSeq.
	rejected := &frame.SendackPacket{ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonRateLimit}
	var rejection *SendackRejectedError
	if err := verifier.HandleSendack(rejected); !errors.As(err, &rejection) {
		t.Fatalf("HandleSendack(rejected) error = %T %v, want SendackRejectedError", err, err)
	}
	if verifier.Snapshot().PendingUnfinished != 1 {
		t.Fatal("retryable SENDACK rejection completed the logical send")
	}
}

func TestVerifierCompletedSendRejectsEveryLaterRejectedSendack(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	success := mustLogicalSend(t, model, 0, 90, TrafficPerson, "sender-secret", "target-secret")
	if err := verifier.RegisterSend(success, time.Unix(1, 0)); err != nil {
		t.Fatalf("RegisterSend(success) error = %v", err)
	}
	successAck := &frame.SendackPacket{MessageID: 91, MessageSeq: 9, ClientMsgNo: success.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
	if err := verifier.HandleSendack(successAck); err != nil {
		t.Fatalf("HandleSendack(success) error = %v", err)
	}
	rejectedSameIdentity := *successAck
	rejectedSameIdentity.ReasonCode = frame.ReasonRateLimit
	assertVerificationCode(t, verifier.HandleSendack(&rejectedSameIdentity), FailureCodeDuplicateCompletion)
	rejectedNoIdentity := &frame.SendackPacket{ClientMsgNo: success.ClientMsgNo, ReasonCode: frame.ReasonRateLimit}
	assertVerificationCode(t, verifier.HandleSendack(rejectedNoIdentity), FailureCodeConflictingCompletion)

	terminal := mustLogicalSend(t, model, 0, 91, TrafficPerson, "sender-secret", "target-secret")
	if err := verifier.RegisterSend(terminal, time.Unix(1, 0)); err != nil {
		t.Fatalf("RegisterSend(terminal) error = %v", err)
	}
	assertVerificationCode(t, verifier.CompleteTerminal(terminal, TerminalSendRetryExhausted), FailureCodeTerminalSend)
	terminalRejected := &frame.SendackPacket{ClientMsgNo: terminal.ClientMsgNo, ReasonCode: frame.ReasonRateLimit}
	assertVerificationCode(t, verifier.HandleSendack(terminalRejected), FailureCodeDuplicateCompletion)
	terminalRejectedWithIdentity := &frame.SendackPacket{MessageID: 92, MessageSeq: 10, ClientMsgNo: terminal.ClientMsgNo, ReasonCode: frame.ReasonRateLimit}
	assertVerificationCode(t, verifier.HandleSendack(terminalRejectedWithIdentity), FailureCodeConflictingCompletion)

	snapshot := verifier.Snapshot()
	if snapshot.DuplicateCompletions != 2 || snapshot.ConflictingCompletions != 2 || snapshot.SendackRejections != 0 {
		t.Fatalf("completed/rejected counters = %+v", snapshot)
	}
}

func TestVerifierIncompleteRejectedSendackRequiresZeroServerIdentity(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	logical := mustLogicalSend(t, model, 0, 92, TrafficPerson, "sender", "target")
	if err := verifier.RegisterSend(logical, time.Unix(1, 0)); err != nil {
		t.Fatalf("RegisterSend() error = %v", err)
	}
	zero := &frame.SendackPacket{ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonRateLimit}
	var rejection *SendackRejectedError
	if err := verifier.HandleSendack(zero); !errors.As(err, &rejection) {
		t.Fatalf("HandleSendack(zero rejected) error = %T %v, want SendackRejectedError", err, err)
	}
	for _, identity := range []struct {
		messageID  int64
		messageSeq uint64
	}{
		{messageID: 1},
		{messageSeq: 1},
		{messageID: 1, messageSeq: 1},
	} {
		ack := &frame.SendackPacket{
			MessageID:   identity.messageID,
			MessageSeq:  identity.messageSeq,
			ClientMsgNo: logical.ClientMsgNo,
			ReasonCode:  frame.ReasonRateLimit,
		}
		assertVerificationCode(t, verifier.HandleSendack(ack), FailureCodeInvalidSendack)
	}
	if snapshot := verifier.Snapshot(); snapshot.SendackRejections != 1 || snapshot.PendingUnfinished != 1 {
		t.Fatalf("rejected identity counters = %+v", snapshot)
	}
}

func TestVerifierValidatesRecvAndAcknowledgesEveryPayloadClass(t *testing.T) {
	model, verifier := newTestVerifier(t, 64, 64, 64, 10*time.Second)
	recipient := "recipient-private"
	sizes := []int{256, 1_024, 4_096, 16_384}
	acker := &recordingRecvAcker{}
	for index, size := range sizes {
		logical := mustLogicalSend(t, model, 0, uint64(20+index), TrafficPerson, "sender-private", recipient)
		payload, err := model.BuildPayload(logical, size)
		if err != nil {
			t.Fatalf("BuildPayload(%d) error = %v", size, err)
		}
		recv := &frame.RecvPacket{
			MessageID:   int64(100 + index),
			MessageSeq:  uint64(200 + index),
			ClientMsgNo: logical.ClientMsgNo,
			ChannelID:   logical.Sender,
			ChannelType: frame.ChannelTypePerson,
			FromUID:     logical.Sender,
			Payload:     payload,
		}
		if err := verifier.HandleRecv(context.Background(), recipient, recv, acker); err != nil {
			t.Fatalf("HandleRecv(%d) error = %v", size, err)
		}
	}
	if len(acker.acks) != len(sizes) {
		t.Fatalf("RECVACK count = %d, want %d", len(acker.acks), len(sizes))
	}
	for index, ack := range acker.acks {
		if ack.MessageID != int64(100+index) || ack.MessageSeq != uint64(200+index) {
			t.Fatalf("ack[%d] = %+v", index, ack)
		}
	}
	snapshot := verifier.Snapshot()
	if snapshot.Received != 4 || snapshot.ReceiveAcknowledged != 4 || snapshot.ReceiveFailures != 0 || snapshot.ReceiveAckFailures != 0 {
		t.Fatalf("receive counters = %+v", snapshot)
	}

	groupLogical := mustLogicalSend(t, model, 1, 99, TrafficGroup, "group-sender", "group-channel")
	groupPayload, err := model.BuildPayload(groupLogical, 256)
	if err != nil {
		t.Fatalf("BuildPayload(group) error = %v", err)
	}
	groupRecv := &frame.RecvPacket{
		MessageID:   999,
		MessageSeq:  1,
		ClientMsgNo: groupLogical.ClientMsgNo,
		ChannelID:   groupLogical.Target,
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     groupLogical.Sender,
		Payload:     groupPayload,
	}
	if err := verifier.HandleRecv(context.Background(), "group-member", groupRecv, acker); err != nil {
		t.Fatalf("HandleRecv(group) error = %v", err)
	}
}

func TestVerifierAcknowledgesButExcludesExternalDemoTraffic(t *testing.T) {
	_, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	acker := &recordingRecvAcker{err: errors.New("external ACK failure must not classify the run")}
	external := &frame.RecvPacket{
		MessageID: 901, MessageSeq: 7, ClientMsgNo: "demo-message", ChannelID: "demo-peer",
		ChannelType: frame.ChannelTypePerson, FromUID: "demo-peer", Payload: []byte("manual Demo payload"),
	}
	if err := verifier.HandleRecv(context.Background(), "workload-recipient", external, acker); err != nil {
		t.Fatalf("external Demo receive error = %v", err)
	}
	if len(acker.acks) != 1 || acker.acks[0].MessageID != external.MessageID || acker.acks[0].MessageSeq != external.MessageSeq {
		t.Fatalf("external Demo ACKs = %+v", acker.acks)
	}
	if snapshot := verifier.Snapshot(); snapshot != (VerifierSnapshot{SendackLatency: newWorkerHistogramSnapshot(), RecvackLatency: newWorkerHistogramSnapshot()}) {
		t.Fatalf("external Demo traffic entered workload counters: %+v", snapshot)
	}

	otherCfg := LocalConfig()
	otherCfg.RunID = "other-demo-run"
	otherIdentity, err := NewIdentitySpace(otherCfg.RunID, otherCfg.Seed, uint64(otherCfg.Workload.Workers))
	if err != nil {
		t.Fatal(err)
	}
	otherModel, err := NewTrafficModel(otherIdentity, otherCfg.Workload)
	if err != nil {
		t.Fatal(err)
	}
	otherLogical := mustLogicalSend(t, otherModel, 0, 1, TrafficPerson, "other-sender", "workload-recipient")
	otherRecv := mustRecvPacket(t, otherModel, otherLogical, 902, 8)
	if err := verifier.HandleRecv(context.Background(), otherLogical.Target, otherRecv, acker); err != nil {
		t.Fatalf("other-run receive error = %v", err)
	}
	if len(acker.acks) != 2 {
		t.Fatalf("other-run ACKs = %+v", acker.acks)
	}
	if snapshot := verifier.Snapshot(); snapshot.Received != 0 || snapshot.ReceiveAcknowledged != 0 || snapshot.ReceiveAckFailures != 0 || snapshot.Corruptions != 0 {
		t.Fatalf("other-run marker entered workload counters: %+v", snapshot)
	}
}

func TestVerifierRecvFailuresRemainProductFailuresAndStillAck(t *testing.T) {
	model, verifier := newTestVerifier(t, 64, 64, 64, 10*time.Second)
	recipient := "recipient-secret"
	logical := mustLogicalSend(t, model, 0, 301, TrafficPerson, "sender-secret", recipient)
	payload, err := model.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	base := frame.RecvPacket{
		MessageID:   301,
		MessageSeq:  10,
		ClientMsgNo: logical.ClientMsgNo,
		ChannelID:   logical.Sender,
		ChannelType: frame.ChannelTypePerson,
		FromUID:     logical.Sender,
		Payload:     payload,
	}

	corrupt := base
	corrupt.MessageSeq = 9
	corrupt.Payload = append([]byte(nil), payload...)
	corrupt.Payload[len(corrupt.Payload)-1] ^= 1
	acker := &recordingRecvAcker{}
	err = verifier.HandleRecv(context.Background(), recipient, &corrupt, acker)
	assertVerificationCode(t, err, FailureCodeReceivePayload)
	if len(acker.acks) != 1 {
		t.Fatalf("corrupt receive ACKs = %d, want 1", len(acker.acks))
	}

	identityMismatch := base
	identityMismatch.MessageSeq = 10
	identityMismatch.ChannelID = "wrong-peer-secret"
	err = verifier.HandleRecv(context.Background(), recipient, &identityMismatch, acker)
	assertVerificationCode(t, err, FailureCodeReceiveIdentity)
	if len(acker.acks) != 2 {
		t.Fatalf("identity failure ACKs = %d, want 2", len(acker.acks))
	}

	valid := base
	valid.MessageSeq = 11
	if err := verifier.HandleRecv(context.Background(), recipient, &valid, acker); err != nil {
		t.Fatalf("HandleRecv(valid) error = %v", err)
	}
	duplicate := base
	duplicate.MessageID++
	duplicate.MessageSeq = 11
	err = verifier.HandleRecv(context.Background(), recipient, &duplicate, acker)
	assertVerificationCode(t, err, FailureCodeReceiveSequence)
	regression := base
	regression.MessageID += 2
	regression.MessageSeq = 10
	err = verifier.HandleRecv(context.Background(), recipient, &regression, acker)
	assertVerificationCode(t, err, FailureCodeReceiveSequence)
	if len(acker.acks) != 5 {
		t.Fatalf("sequence failure ACKs = %d, want 5", len(acker.acks))
	}
	snapshot := verifier.Snapshot()
	if snapshot.Classification != SyncClassificationProductFailure {
		t.Fatal("receive correctness failure did not stick as product_failure")
	}
	if snapshot.DuplicateDeliveries != 0 || snapshot.ConflictingDeliveries != 1 || snapshot.SequenceRegressions != 1 {
		t.Fatalf("delivery counters = %+v", snapshot)
	}
	if snapshot.Corruptions != 1 {
		t.Fatalf("Corruptions = %d, want one payload corruption", snapshot.Corruptions)
	}
	if snapshot.ReceiveFailures != 4 {
		t.Fatalf("ReceiveFailures = %d, want 4 validation failures from 5 received packets", snapshot.ReceiveFailures)
	}
	evidenceJSON, marshalErr := json.Marshal(verifier.EvidenceSnapshot())
	if marshalErr != nil {
		t.Fatalf("Marshal(EvidenceSnapshot) error = %v", marshalErr)
	}
	for _, secret := range []string{recipient, logical.Sender, logical.ClientMsgNo, string(payload)} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("receive error leaks secret %q", secret)
		}
		if strings.Contains(string(evidenceJSON), secret) {
			t.Fatalf("evidence leaks secret %q: %s", secret, evidenceJSON)
		}
	}
}

func TestVerifierCountsOnlyConfirmedExactRetransmissionAsDuplicate(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Minute)
	logical := mustLogicalSend(t, model, 0, 311, TrafficPerson, "sender", "recipient")
	recv := mustRecvPacket(t, model, logical, 311, 1)
	acker := &recordingRecvAcker{}
	if err := verifier.HandleRecv(context.Background(), logical.Target, recv, acker); err != nil {
		t.Fatalf("HandleRecv(first): %v", err)
	}
	assertVerificationCode(t, verifier.HandleRecv(context.Background(), logical.Target, recv, acker), FailureCodeReceiveSequence)

	snapshot := verifier.Snapshot()
	if snapshot.DuplicateDeliveries != 1 || snapshot.Duplicates != 1 ||
		snapshot.ConflictingDeliveries != 0 || snapshot.SequenceRegressions != 0 {
		t.Fatalf("duplicate counters = %+v", snapshot)
	}
}

func TestVerifierRecvackFailureIsRecordedWithoutRawErrorAndProtocolInvalidIsNotAcked(t *testing.T) {
	model, verifier := newTestVerifier(t, 64, 64, 64, 10*time.Second)
	recipient := "recipient"
	logical := mustLogicalSend(t, model, 0, 401, TrafficPerson, "sender", recipient)
	payload, err := model.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	recv := &frame.RecvPacket{
		MessageID:   401,
		MessageSeq:  1,
		ClientMsgNo: logical.ClientMsgNo,
		ChannelID:   logical.Sender,
		ChannelType: frame.ChannelTypePerson,
		FromUID:     logical.Sender,
		Payload:     payload,
	}
	rawTransportError := "dial failed with token-and-private-address"
	acker := &recordingRecvAcker{err: errors.New(rawTransportError)}
	err = verifier.HandleRecv(context.Background(), recipient, recv, acker)
	assertVerificationCode(t, err, FailureCodeRecvackUnclassified)
	if strings.Contains(err.Error(), rawTransportError) {
		t.Fatalf("recvack error leaks transport error: %q", err)
	}
	snapshot := verifier.Snapshot()
	if snapshot.Received != 1 || snapshot.ReceiveAckFailures != 1 || snapshot.ReceiveAckHarnessFailures != 1 || snapshot.ReceiveAcknowledged != 0 {
		t.Fatalf("recvack failure counters = %+v", snapshot)
	}
	corrupt := *recv
	corrupt.MessageID++
	corrupt.MessageSeq++
	corrupt.Payload = append([]byte(nil), recv.Payload...)
	corrupt.Payload[len(corrupt.Payload)-1] ^= 1
	err = verifier.HandleRecv(context.Background(), recipient, &corrupt, acker)
	assertVerificationCode(t, err, FailureCodeReceivePayload)
	if snapshot = verifier.Snapshot(); snapshot.ReceiveFailures != 1 || snapshot.ReceiveAckFailures != 2 {
		t.Fatalf("combined validation/recvack counters = %+v", snapshot)
	}

	invalid := *recv
	invalid.MessageID = 0
	assertVerificationCode(t, verifier.HandleRecv(context.Background(), recipient, &invalid, acker), FailureCodeReceiveProtocol)
	if len(acker.acks) != 2 {
		t.Fatalf("protocol-invalid receive was ACKed: %d ACK calls", len(acker.acks))
	}
}

func TestVerifierRecvWithPositiveServerIdentityAndEmptyClientMessageNumberIsStillAcked(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	logical := mustLogicalSend(t, model, 0, 450, TrafficPerson, "sender-secret", "recipient-secret")
	recv := mustRecvPacket(t, model, logical, 450, 1)
	recv.ClientMsgNo = ""
	acker := &recordingRecvAcker{}
	err := verifier.HandleRecv(context.Background(), logical.Target, recv, acker)
	assertVerificationCode(t, err, FailureCodeReceiveIdentity)
	if len(acker.acks) != 1 || acker.acks[0].MessageID != recv.MessageID || acker.acks[0].MessageSeq != recv.MessageSeq {
		t.Fatalf("empty-client-msg RECVACKs = %+v, want one exact ACK", acker.acks)
	}
	if snapshot := verifier.Snapshot(); snapshot.Received != 1 || snapshot.ReceiveFailures != 1 || snapshot.ReceiveAcknowledged != 1 {
		t.Fatalf("empty-client-msg counters = %+v", snapshot)
	}
	encoded, marshalErr := json.Marshal(verifier.EvidenceSnapshot())
	if marshalErr != nil {
		t.Fatalf("Marshal(EvidenceSnapshot) error = %v", marshalErr)
	}
	for _, secret := range []string{logical.Sender, logical.Target, string(recv.Payload)} {
		if strings.Contains(err.Error(), secret) || strings.Contains(string(encoded), secret) {
			t.Fatalf("empty-client-msg failure leaks %q", secret)
		}
	}
}

func TestVerifierUnconfirmedRecvRetransmitRetriesAckWithoutDuplicate(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	logical := mustLogicalSend(t, model, 0, 460, TrafficPerson, "sender", "recipient")
	recv := mustRecvPacket(t, model, logical, 460, 7)
	acker := &scriptedRecvAcker{errs: []error{errors.New("raw local transport secret"), nil}}

	firstErr := verifier.HandleRecv(context.Background(), logical.Target, recv, acker)
	assertVerificationCode(t, firstErr, FailureCodeRecvackUnclassified)
	var firstVerification *VerificationError
	if !errors.As(firstErr, &firstVerification) || firstVerification.Classification() != SyncClassificationHarnessInvalid {
		t.Fatalf("first ACK failure = %v, want harness_invalid", firstErr)
	}
	if err := verifier.HandleRecv(context.Background(), logical.Target, recv, acker); err != nil {
		t.Fatalf("HandleRecv(exact retransmit) error = %v", err)
	}
	snapshot := verifier.Snapshot()
	if snapshot.Received != 2 || snapshot.ReceiveAcknowledged != 1 || snapshot.ReceiveAckFailures != 1 || snapshot.ReceiveAckHarnessFailures != 1 {
		t.Fatalf("retransmit counters = %+v", snapshot)
	}
	if snapshot.DuplicateDeliveries != 0 || snapshot.SequenceRegressions != 0 || snapshot.ConflictingDeliveries != 0 || snapshot.ReceiveFailures != 0 {
		t.Fatalf("exact unconfirmed retransmit became correctness failure: %+v", snapshot)
	}
	if snapshot.Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("classification = %q, want harness_invalid", snapshot.Classification)
	}
}

func TestVerifierUnconfirmedRecvSameSequenceDifferentIdentityIsProductConflict(t *testing.T) {
	model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
	first := mustLogicalSend(t, model, 0, 470, TrafficPerson, "sender", "recipient")
	second := mustLogicalSend(t, model, 0, 471, TrafficPerson, "sender", "recipient")
	firstRecv := mustRecvPacket(t, model, first, 470, 8)
	secondRecv := mustRecvPacket(t, model, second, 471, 8)
	acker := &scriptedRecvAcker{errs: []error{errors.New("raw local failure"), nil}}
	assertVerificationCode(t, verifier.HandleRecv(context.Background(), first.Target, firstRecv, acker), FailureCodeRecvackUnclassified)
	conflictErr := verifier.HandleRecv(context.Background(), second.Target, secondRecv, acker)
	assertVerificationCode(t, conflictErr, FailureCodeReceiveSequence)
	snapshot := verifier.Snapshot()
	if snapshot.ConflictingDeliveries != 1 || snapshot.DuplicateDeliveries != 0 || snapshot.SequenceRegressions != 0 {
		t.Fatalf("same-sequence conflict counters = %+v", snapshot)
	}
	if snapshot.Classification != SyncClassificationProductFailure {
		t.Fatalf("classification = %q, want product_failure", snapshot.Classification)
	}
}

func TestVerifierRecvackFailureClassificationIsClosedAndRedacted(t *testing.T) {
	tests := []struct {
		name        string
		ackErr      error
		rawSecret   string
		wantCode    FailureCode
		wantClass   SyncClassification
		wantProduct uint64
		wantHarness uint64
	}{
		{name: "context canceled", ackErr: context.Canceled, wantCode: FailureCodeRecvackCanceled, wantClass: SyncClassificationHarnessInvalid, wantHarness: 1},
		{name: "deadline exceeded", ackErr: context.DeadlineExceeded, wantCode: FailureCodeRecvackDeadline, wantClass: SyncClassificationHarnessInvalid, wantHarness: 1},
		{name: "unclassified transport", ackErr: errors.New("raw transport with token secret"), rawSecret: "raw transport with token secret", wantCode: FailureCodeRecvackUnclassified, wantClass: SyncClassificationHarnessInvalid, wantHarness: 1},
		{name: "explicit product", ackErr: NewProductRecvAckError(ProductRecvAckRejected, errors.New("raw target body secret")), rawSecret: "raw target body secret", wantCode: FailureCodeRecvack, wantClass: SyncClassificationProductFailure, wantProduct: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, verifier := newTestVerifier(t, 16, 16, 16, time.Second)
			logical := mustLogicalSend(t, model, 0, 480, TrafficPerson, "sender-secret", "recipient-secret")
			recv := mustRecvPacket(t, model, logical, 480, 9)
			err := verifier.HandleRecv(context.Background(), logical.Target, recv, &recordingRecvAcker{err: test.ackErr})
			assertVerificationCode(t, err, test.wantCode)
			var verification *VerificationError
			if !errors.As(err, &verification) || verification.Classification() != test.wantClass {
				t.Fatalf("classification = %v, want %q", err, test.wantClass)
			}
			snapshot := verifier.Snapshot()
			if snapshot.ReceiveAckProductFailures != test.wantProduct || snapshot.ReceiveAckHarnessFailures != test.wantHarness {
				t.Fatalf("ACK ownership counters = %+v", snapshot)
			}
			encoded, marshalErr := json.Marshal(verifier.EvidenceSnapshot())
			if marshalErr != nil {
				t.Fatalf("Marshal(EvidenceSnapshot) error = %v", marshalErr)
			}
			for _, secret := range []string{"sender-secret", "recipient-secret", test.rawSecret} {
				if secret == "" {
					continue
				}
				if strings.Contains(err.Error(), secret) || strings.Contains(string(encoded), secret) {
					t.Fatalf("ACK failure leaks %q", secret)
				}
			}
		})
	}
}

func TestVerifierCorrelationSamplesExactlyOnePercentAndPhysicallyRemovesCompletedHistory(t *testing.T) {
	model, verifier := newTestVerifier(t, 2_000, 16, 64, 10*time.Second)
	acker := discardRecvAcker{}
	recipient := "recipient"
	started := time.Unix(1_000, 0)
	const sends = 10_000
	for ordinal := uint64(0); ordinal < sends; ordinal++ {
		logical := mustLogicalSend(t, model, 0, ordinal, TrafficPerson, "sender", recipient)
		if err := verifier.RegisterSend(logical, started.Add(time.Duration(ordinal)*time.Millisecond)); err != nil {
			t.Fatalf("RegisterSend(%d) error = %v", ordinal, err)
		}
		payload, err := model.BuildPayload(logical, 256)
		if err != nil {
			t.Fatalf("BuildPayload(%d) error = %v", ordinal, err)
		}
		ack := &frame.SendackPacket{MessageID: int64(ordinal + 1), MessageSeq: ordinal + 1, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
		recv := &frame.RecvPacket{
			MessageID:   int64(ordinal + 1),
			MessageSeq:  ordinal + 1,
			ClientMsgNo: logical.ClientMsgNo,
			ChannelID:   logical.Sender,
			ChannelType: frame.ChannelTypePerson,
			FromUID:     logical.Sender,
			Payload:     payload,
		}
		if ordinal%2 == 0 {
			if err := verifier.HandleRecv(context.Background(), recipient, recv, acker); err != nil {
				t.Fatalf("HandleRecv before ACK (%d) error = %v", ordinal, err)
			}
			if err := verifier.HandleSendack(ack); err != nil {
				t.Fatalf("HandleSendack after RECV (%d) error = %v", ordinal, err)
			}
		} else {
			if err := verifier.HandleSendack(ack); err != nil {
				t.Fatalf("HandleSendack before RECV (%d) error = %v", ordinal, err)
			}
			if err := verifier.HandleRecv(context.Background(), recipient, recv, acker); err != nil {
				t.Fatalf("HandleRecv after ACK (%d) error = %v", ordinal, err)
			}
		}
		if err := verifier.ReleaseSend(logical); err != nil {
			t.Fatalf("ReleaseSend(%d) error = %v", ordinal, err)
		}
	}

	snapshot := verifier.Snapshot()
	if snapshot.Sampled != 100 || snapshot.SampledDelivered != 100 {
		t.Fatalf("sampling counts = sampled %d delivered %d, want 100/100", snapshot.Sampled, snapshot.SampledDelivered)
	}
	if snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 || snapshot.PendingCurrent != 0 {
		t.Fatalf("completed history retained = %+v", snapshot)
	}
	if snapshot.CorrelationPeak > 1 {
		t.Fatalf("sequential correlation peak = %d, want <= 1", snapshot.CorrelationPeak)
	}
	if released := verifier.ReleaseRecipient(recipient); released != 1 {
		t.Fatalf("ReleaseRecipient() = %d, want 1 channel state", released)
	}
	if verifier.Snapshot().SequenceCurrent != 0 {
		t.Fatal("recipient sequence state survived logout release")
	}
}

func TestVerifierCorrelationSamplesExactlyOnceInEveryWorkerOrdinalBlock(t *testing.T) {
	starts := []uint64{0, 100, 37, 1_234}
	for worker := uint64(0); worker < 3; worker++ {
		for _, start := range starts {
			name := "worker-" + strconv.FormatUint(worker, 10) + "-start-" + strconv.FormatUint(start, 10)
			t.Run(name, func(t *testing.T) {
				model, verifier := newTestVerifier(t, 128, 16, 4, 5*time.Second)
				for ordinal := start; ordinal < start+100; ordinal++ {
					logical := mustLogicalSend(t, model, worker, ordinal, TrafficPerson, "sender", "recipient")
					if err := verifier.RegisterSend(logical, time.Unix(2_000, 0)); err != nil {
						t.Fatalf("RegisterSend(%d) error = %v", ordinal, err)
					}
				}
				snapshot := verifier.Snapshot()
				if snapshot.Sampled != 1 || snapshot.CorrelationCurrent != 1 || snapshot.DeadlineCurrent != 1 {
					t.Fatalf("worker %d block [%d,%d) sampling = %+v, want exactly one", worker, start, start+100, snapshot)
				}
			})
		}
	}
}

func TestVerifierCorrelationCycleIgnoresGenerationAndIdentityDomainPrefixes(t *testing.T) {
	_, verifier := newTestVerifier(t, 128, 16, 4, 5*time.Second)
	sampled := 0
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		domain := LogicalDomain(ordinal%uint64(LogicalDomainCanary) + 1)
		scoped, err := scopedLogicalOrdinal(7, domain, ordinal)
		if err != nil {
			t.Fatalf("scopedLogicalOrdinal(%d): %v", ordinal, err)
		}
		correlate, err := verifier.ShouldCorrelate(LogicalSend{LogicalSend: scoped, WorkerID: 0})
		if err != nil {
			t.Fatalf("ShouldCorrelate(%d): %v", ordinal, err)
		}
		if correlate {
			sampled++
		}
	}
	if sampled != 1 {
		t.Fatalf("mixed generation/domain block sampled = %d, want exactly 1", sampled)
	}
}

func TestVerifierSampledCorrelationSurvivesSendReleaseUntilLaterRecv(t *testing.T) {
	model, verifier := newTestVerifier(t, 128, 16, 4, 5*time.Second)
	logical := firstSampledLogical(t, model, verifier, "sender", "recipient", time.Unix(2_200, 0))
	ack := &frame.SendackPacket{MessageID: 220, MessageSeq: 22, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
	if err := verifier.HandleSendack(ack); err != nil {
		t.Fatalf("HandleSendack() error = %v", err)
	}
	if err := verifier.ReleaseSend(logical); err != nil {
		t.Fatalf("ReleaseSend() error = %v", err)
	}
	before := verifier.Snapshot()
	if before.CorrelationCurrent != 1 || before.DeadlineCurrent != 1 || before.SampledDelivered != 0 {
		t.Fatalf("correlation after ReleaseSend = %+v", before)
	}
	recv := mustRecvPacket(t, model, logical, 220, 22)
	if err := verifier.HandleRecv(context.Background(), logical.Target, recv, discardRecvAcker{}); err != nil {
		t.Fatalf("HandleRecv() error = %v", err)
	}
	after := verifier.Snapshot()
	if after.CorrelationCurrent != 0 || after.DeadlineCurrent != 0 || after.SampledDelivered != 1 {
		t.Fatalf("correlation after later RECV = %+v", after)
	}
}

func TestVerifierCorrelationExpiryIsConfirmedLossAndDrainIsBounded(t *testing.T) {
	model, verifier := newTestVerifier(t, 500, 16, 8, 5*time.Second)
	started := time.Unix(2_000, 0)
	for ordinal := uint64(0); ordinal < 200; ordinal++ {
		logical := mustLogicalSend(t, model, 0, ordinal, TrafficPerson, "sender", "recipient")
		if err := verifier.RegisterSend(logical, started); err != nil {
			t.Fatalf("RegisterSend(%d) error = %v", ordinal, err)
		}
	}
	before := verifier.Snapshot()
	if before.Sampled != 2 || before.CorrelationCurrent != 2 || before.DeadlineCurrent != 2 {
		t.Fatalf("before expiry = %+v", before)
	}
	if expired := verifier.ExpireCorrelations(started.Add(5 * time.Second)); expired != 2 {
		t.Fatalf("ExpireCorrelations() = %d, want 2", expired)
	}
	after := verifier.Snapshot()
	if after.SampledExpired != 2 || after.Losses != 2 || after.CorrelationCurrent != 0 || after.DeadlineCurrent != 0 {
		t.Fatalf("after expiry = %+v", after)
	}
	if after.Classification != SyncClassificationProductFailure {
		t.Fatalf("classification = %q, want product_failure", after.Classification)
	}
	drain := verifier.DrainSnapshot()
	if drain.PendingUnfinished != 200 || drain.CorrelationOutstanding != 0 || !drain.NextCorrelationDeadline.IsZero() {
		t.Fatalf("drain = %+v", drain)
	}
}

func TestVerifierSampledTerminalSendRemainsUntilCorrelationDeadline(t *testing.T) {
	model, verifier := newTestVerifier(t, 200, 16, 16, 5*time.Second)
	started := time.Unix(2_500, 0)
	logical := firstSampledLogical(t, model, verifier, "sender", "recipient", started)
	assertVerificationCode(t, verifier.CompleteTerminal(logical, TerminalSendRetryExhausted), FailureCodeTerminalSend)
	before := verifier.Snapshot()
	if before.CorrelationCurrent != 1 || before.DeadlineCurrent != 1 || before.SampledExpired != 0 {
		t.Fatalf("sampled terminal before deadline = %+v", before)
	}
	if expired := verifier.ExpireCorrelations(started.Add(5 * time.Second)); expired != 1 {
		t.Fatalf("ExpireCorrelations() = %d, want 1", expired)
	}
	after := verifier.Snapshot()
	if after.CorrelationCurrent != 0 || after.DeadlineCurrent != 0 || after.SampledExpired != 1 {
		t.Fatalf("sampled terminal after deadline = %+v", after)
	}
	evidenceCount := evidenceCountForClass(verifier.EvidenceSnapshot(), FailureClassCorrelation)
	if expired := verifier.ExpireCorrelations(started.Add(10 * time.Second)); expired != 0 {
		t.Fatalf("second ExpireCorrelations() = %d, want 0", expired)
	}
	again := verifier.Snapshot()
	if again.SampledExpired != 1 || evidenceCountForClass(verifier.EvidenceSnapshot(), FailureClassCorrelation) != evidenceCount {
		t.Fatalf("second expiry changed counters/evidence: snapshot=%+v evidence=%+v", again, verifier.EvidenceSnapshot())
	}
}

func TestVerifierSampledTerminalLateMatchingAckCompletesCorrelationInBothOrders(t *testing.T) {
	tests := []struct {
		name      string
		release   bool
		recvFirst bool
		wantCode  FailureCode
	}{
		{name: "terminal recv then ack", recvFirst: true, wantCode: FailureCodeConflictingCompletion},
		{name: "terminal ack then recv", recvFirst: false, wantCode: FailureCodeConflictingCompletion},
		{name: "released terminal recv then ack", release: true, recvFirst: true, wantCode: FailureCodeUnknownSendack},
		{name: "released terminal ack then recv", release: true, recvFirst: false, wantCode: FailureCodeUnknownSendack},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, verifier := newTestVerifier(t, 200, 16, 4, 5*time.Second)
			started := time.Unix(2_700, 0)
			logical := firstSampledLogical(t, model, verifier, "sender", "recipient", started)
			assertVerificationCode(t, verifier.CompleteTerminal(logical, TerminalSendRetryExhausted), FailureCodeTerminalSend)
			if test.release {
				if err := verifier.ReleaseSend(logical); err != nil {
					t.Fatalf("ReleaseSend() error = %v", err)
				}
			}
			ack := &frame.SendackPacket{MessageID: 900, MessageSeq: 9, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
			recv := mustRecvPacket(t, model, logical, 900, 9)
			if test.recvFirst {
				if err := verifier.HandleRecv(context.Background(), logical.Target, recv, discardRecvAcker{}); err != nil {
					t.Fatalf("HandleRecv(before late ACK) error = %v", err)
				}
				assertVerificationCode(t, verifier.HandleSendack(ack), test.wantCode)
			} else {
				assertVerificationCode(t, verifier.HandleSendack(ack), test.wantCode)
				if err := verifier.HandleRecv(context.Background(), logical.Target, recv, discardRecvAcker{}); err != nil {
					t.Fatalf("HandleRecv(after late ACK) error = %v", err)
				}
			}
			snapshot := verifier.Snapshot()
			if snapshot.SampledDelivered != 1 || snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 {
				t.Fatalf("late ACK correlation = %+v", snapshot)
			}
			if expired := verifier.ExpireCorrelations(started.Add(10 * time.Second)); expired != 0 {
				t.Fatalf("ExpireCorrelations() = %d, want 0", expired)
			}
			if evidenceCountForClass(verifier.EvidenceSnapshot(), FailureClassCorrelation) != 0 {
				t.Fatalf("matching late ACK created correlation evidence: %+v", verifier.EvidenceSnapshot())
			}
		})
	}
}

func TestVerifierReleasedRegisteredAttemptLateAckCompletesSampledCorrelation(t *testing.T) {
	model, verifier := newTestVerifier(t, 200, 16, 4, 5*time.Second)
	started := time.Unix(2_750, 0)
	logical := firstSampledLogical(t, model, verifier, "sender", "recipient", started)
	policy := newTestRetryPolicy(t, model)
	attempt, err := policy.Attempt(logical, 0)
	if err != nil {
		t.Fatalf("Attempt(0): %v", err)
	}
	const clientSeq = uint64(701)
	if err := verifier.ObserveAttempt(logical, attempt, clientSeq); err != nil {
		t.Fatalf("ObserveAttempt: %v", err)
	}
	assertVerificationCode(t, verifier.CompleteTerminal(logical, TerminalSendRetryExhausted), FailureCodeTerminalSend)
	if err := verifier.ReleaseSend(logical); err != nil {
		t.Fatalf("ReleaseSend: %v", err)
	}
	if snapshot := verifier.Snapshot(); snapshot.ReleasedAttemptCurrent != 1 {
		t.Fatalf("released attempt current = %d, want 1", snapshot.ReleasedAttemptCurrent)
	}

	const messageID, messageSeq = int64(905), uint64(15)
	if err := verifier.HandleRecv(context.Background(), logical.Target, mustRecvPacket(t, model, logical, messageID, messageSeq), discardRecvAcker{}); err != nil {
		t.Fatalf("HandleRecv: %v", err)
	}
	ack := &frame.SendackPacket{
		ClientSeq: clientSeq, ClientMsgNo: logical.ClientMsgNo,
		MessageID: messageID, MessageSeq: messageSeq, ReasonCode: frame.ReasonSuccess,
	}
	if err := verifier.HandleSendack(ack); err != nil {
		t.Fatalf("HandleSendack(released registered attempt): %v", err)
	}
	if expired := verifier.ExpireCorrelations(started.Add(10 * time.Second)); expired != 0 {
		t.Fatalf("ExpireCorrelations = %d, want 0 after matching late ACK", expired)
	}
	snapshot := verifier.Snapshot()
	if snapshot.SampledDelivered != 1 || snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 || snapshot.ReleasedAttemptCurrent != 0 {
		t.Fatalf("released attempt correlation = %+v", snapshot)
	}
	if snapshot.Terminal != 1 || snapshot.Classification != SyncClassificationProductFailure {
		t.Fatalf("terminal precedence = %+v", snapshot)
	}
	if evidenceCountForClass(verifier.EvidenceSnapshot(), FailureClassCorrelation) != 0 {
		t.Fatalf("matching late ACK created correlation evidence: %+v", verifier.EvidenceSnapshot())
	}
}

func TestVerifierSampledTerminalLateConflictingAckRecordsCorrelationConflict(t *testing.T) {
	model, verifier := newTestVerifier(t, 200, 16, 4, 5*time.Second)
	started := time.Unix(2_800, 0)
	logical := firstSampledLogical(t, model, verifier, "sender", "recipient", started)
	assertVerificationCode(t, verifier.CompleteTerminal(logical, TerminalSendRetryExhausted), FailureCodeTerminalSend)
	if err := verifier.HandleRecv(context.Background(), logical.Target, mustRecvPacket(t, model, logical, 910, 10), discardRecvAcker{}); err != nil {
		t.Fatalf("HandleRecv() error = %v", err)
	}
	conflicting := &frame.SendackPacket{MessageID: 911, MessageSeq: 10, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
	assertVerificationCode(t, verifier.HandleSendack(conflicting), FailureCodeCorrelationSequenceConflict)
	snapshot := verifier.Snapshot()
	if snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 || evidenceCountForClass(verifier.EvidenceSnapshot(), FailureClassCorrelation) != 1 {
		t.Fatalf("late conflicting ACK correlation = %+v evidence=%+v", snapshot, verifier.EvidenceSnapshot())
	}
	if expired := verifier.ExpireCorrelations(started.Add(10 * time.Second)); expired != 0 {
		t.Fatalf("ExpireCorrelations() = %d, want 0", expired)
	}
}

func TestVerifierSampledRepeatedAckWithConflictingIdentityRecordsCorrelationConflict(t *testing.T) {
	model, verifier := newTestVerifier(t, 200, 16, 4, 5*time.Second)
	logical := firstSampledLogical(t, model, verifier, "sender", "recipient", time.Unix(2_900, 0))
	first := &frame.SendackPacket{MessageID: 920, MessageSeq: 11, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
	if err := verifier.HandleSendack(first); err != nil {
		t.Fatalf("HandleSendack(first) error = %v", err)
	}
	conflicting := *first
	conflicting.MessageID++
	assertVerificationCode(t, verifier.HandleSendack(&conflicting), FailureCodeCorrelationSequenceConflict)
	if snapshot := verifier.Snapshot(); snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 || evidenceCountForClass(verifier.EvidenceSnapshot(), FailureClassCorrelation) != 1 {
		t.Fatalf("repeated conflicting ACK correlation = %+v evidence=%+v", snapshot, verifier.EvidenceSnapshot())
	}
}

func TestVerifierCapacityOverflowIsHarnessInvalidAndSequenceOwnershipCanBeReleased(t *testing.T) {
	model, verifier := newTestVerifier(t, 500, 1, 1, 5*time.Second)
	started := time.Unix(3_000, 0)
	var correlationOverflow error
	for ordinal := uint64(0); ordinal < 200; ordinal++ {
		logical := mustLogicalSend(t, model, 0, ordinal, TrafficPerson, "sender", "recipient")
		err := verifier.RegisterSend(logical, started)
		if err != nil {
			correlationOverflow = err
			break
		}
	}
	assertVerificationCode(t, correlationOverflow, FailureCodeCorrelationCapacity)
	var verification *VerificationError
	if !errors.As(correlationOverflow, &verification) || verification.Classification() != SyncClassificationHarnessInvalid {
		t.Fatalf("correlation overflow = %v, want harness_invalid", correlationOverflow)
	}
	if verifier.Snapshot().Classification != SyncClassificationHarnessInvalid {
		t.Fatal("capacity overflow did not set harness_invalid")
	}

	// Use a fresh verifier so product evidence from unrelated checks cannot mask
	// the sequence-capacity ownership behavior.
	model, verifier = newTestVerifier(t, 16, 1, 16, 5*time.Second)
	acker := &recordingRecvAcker{}
	first := mustLogicalSend(t, model, 0, 700, TrafficPerson, "sender-a", "recipient")
	firstRecv := mustRecvPacket(t, model, first, 1, 1)
	if err := verifier.HandleRecv(context.Background(), first.Target, firstRecv, acker); err != nil {
		t.Fatalf("HandleRecv(first) error = %v", err)
	}
	second := mustLogicalSend(t, model, 0, 701, TrafficPerson, "sender-b", "recipient")
	secondRecv := mustRecvPacket(t, model, second, 2, 1)
	err := verifier.HandleRecv(context.Background(), second.Target, secondRecv, acker)
	assertVerificationCode(t, err, FailureCodeSequenceCapacity)
	if len(acker.acks) != 2 {
		t.Fatalf("sequence-capacity receive ACKs = %d, want 2", len(acker.acks))
	}
	if released := verifier.ReleaseRecipient("recipient"); released != 1 {
		t.Fatalf("ReleaseRecipient() = %d, want 1", released)
	}
	if err := verifier.HandleRecv(context.Background(), second.Target, secondRecv, acker); err != nil {
		t.Fatalf("HandleRecv(after release) error = %v", err)
	}
}

func TestVerifierSampledRecvCompletesBeforeSequenceCapacityAdmission(t *testing.T) {
	model, verifier := newTestVerifier(t, 200, 1, 4, 5*time.Second)
	started := time.Unix(3_500, 0)
	fill := mustLogicalSend(t, model, 0, 750, TrafficPerson, "fill-sender", "fill-recipient")
	if err := verifier.HandleRecv(context.Background(), fill.Target, mustRecvPacket(t, model, fill, 750, 1), discardRecvAcker{}); err != nil {
		t.Fatalf("HandleRecv(fill sequence capacity) error = %v", err)
	}

	logical := firstSampledLogical(t, model, verifier, "sampled-sender", "sampled-recipient", started)
	ack := &frame.SendackPacket{MessageID: 751, MessageSeq: 1, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
	if err := verifier.HandleSendack(ack); err != nil {
		t.Fatalf("HandleSendack(sampled) error = %v", err)
	}
	acker := &recordingRecvAcker{}
	recvErr := verifier.HandleRecv(context.Background(), logical.Target, mustRecvPacket(t, model, logical, 751, 1), acker)
	assertVerificationCode(t, recvErr, FailureCodeSequenceCapacity)
	var verification *VerificationError
	if !errors.As(recvErr, &verification) || verification.Classification() != SyncClassificationHarnessInvalid {
		t.Fatalf("sequence capacity error = %v, want harness_invalid", recvErr)
	}
	if len(acker.acks) != 1 {
		t.Fatalf("sequence-capacity RECVACK count = %d, want 1", len(acker.acks))
	}
	snapshot := verifier.Snapshot()
	if snapshot.SampledDelivered != 1 || snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 {
		t.Fatalf("sequence capacity blocked correlation = %+v", snapshot)
	}
	if expired := verifier.ExpireCorrelations(started.Add(10 * time.Second)); expired != 0 {
		t.Fatalf("ExpireCorrelations() = %d, want 0", expired)
	}
	if evidenceCountForClass(verifier.EvidenceSnapshot(), FailureClassCorrelation) != 0 || verifier.Snapshot().Classification != SyncClassificationHarnessInvalid {
		t.Fatalf("sequence capacity created false loss/product evidence: snapshot=%+v evidence=%+v", verifier.Snapshot(), verifier.EvidenceSnapshot())
	}
}

func TestVerifierPendingCapacityIsHarnessInvalidWithoutPartialRegistration(t *testing.T) {
	model, verifier := newTestVerifier(t, 1, 16, 16, 5*time.Second)
	first := mustLogicalSend(t, model, 0, 800, TrafficPerson, "sender-a", "recipient")
	second := mustLogicalSend(t, model, 0, 801, TrafficPerson, "sender-b", "recipient")
	if err := verifier.RegisterSend(first, time.Unix(5_000, 0)); err != nil {
		t.Fatalf("RegisterSend(first) error = %v", err)
	}
	err := verifier.RegisterSend(second, time.Unix(5_000, 0))
	assertVerificationCode(t, err, FailureCodePendingCapacity)
	var verification *VerificationError
	if !errors.As(err, &verification) || verification.Classification() != SyncClassificationHarnessInvalid {
		t.Fatalf("pending overflow = %v, want harness_invalid", err)
	}
	snapshot := verifier.Snapshot()
	if snapshot.Sent != 1 || snapshot.PendingCurrent != 1 || snapshot.PendingUnfinished != 1 {
		t.Fatalf("failed registration partially mutated state: %+v", snapshot)
	}
}

func TestVerifierRejectsInvalidOrExtremeStateBounds(t *testing.T) {
	model := newTestTrafficModel(t, FormalConfig())
	evidence, err := NewEvidenceRecorder(1, 1)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder() error = %v", err)
	}
	valid := VerifierConfig{PendingCapacity: 1, SequenceCapacity: 1, CorrelationCapacity: 1, CorrelationDeadline: time.Second}
	tests := []struct {
		name     string
		model    TrafficModel
		config   VerifierConfig
		evidence *EvidenceRecorder
	}{
		{name: "missing model", config: valid, evidence: evidence},
		{name: "missing evidence", model: model, config: valid},
		{name: "zero pending", model: model, config: func() VerifierConfig { c := valid; c.PendingCapacity = 0; return c }(), evidence: evidence},
		{name: "negative sequence", model: model, config: func() VerifierConfig { c := valid; c.SequenceCapacity = -1; return c }(), evidence: evidence},
		{name: "extreme correlation", model: model, config: func() VerifierConfig { c := valid; c.CorrelationCapacity = maxVerifierCapacity + 1; return c }(), evidence: evidence},
		{name: "zero deadline", model: model, config: func() VerifierConfig { c := valid; c.CorrelationDeadline = 0; return c }(), evidence: evidence},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewVerifier(test.model, test.config, test.evidence); !errors.Is(err, errVerifierConfig) {
				t.Fatalf("NewVerifier() error = %v, want %v", err, errVerifierConfig)
			}
		})
	}
}

func TestVerifierCorrelationRejectsConflictingServerSequence(t *testing.T) {
	model, verifier := newTestVerifier(t, 200, 16, 16, 5*time.Second)
	logical := firstSampledLogical(t, model, verifier, "sender", "recipient", time.Unix(4_000, 0))
	recv := mustRecvPacket(t, model, logical, 44, 8)
	acker := &recordingRecvAcker{}
	if err := verifier.HandleRecv(context.Background(), logical.Target, recv, acker); err != nil {
		t.Fatalf("HandleRecv() error = %v", err)
	}
	ack := &frame.SendackPacket{MessageID: 44, MessageSeq: 9, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
	assertVerificationCode(t, verifier.HandleSendack(ack), FailureCodeCorrelationSequenceConflict)
	snapshot := verifier.Snapshot()
	if snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 {
		t.Fatalf("conflicting correlation retained = %+v", snapshot)
	}
	if snapshot.ConflictingCompletions != 1 {
		t.Fatalf("conflicting completion count = %d, want 1", snapshot.ConflictingCompletions)
	}
}

func TestVerifierConcurrentSendRecvAndSnapshotsAreRaceSafe(t *testing.T) {
	model, verifier := newTestVerifier(t, 256, 256, 16, 5*time.Second)
	const sends = 100
	logicals := make([]LogicalSend, sends)
	recvs := make([]*frame.RecvPacket, sends)
	for ordinal := uint64(0); ordinal < sends; ordinal++ {
		suffix := strconv.FormatUint(ordinal, 10)
		logicals[ordinal] = mustLogicalSend(t, model, 0, ordinal, TrafficPerson, "sender-"+suffix, "recipient-"+suffix)
		recvs[ordinal] = mustRecvPacket(t, model, logicals[ordinal], int64(ordinal+1), 1)
	}

	var register sync.WaitGroup
	errs := make(chan error, sends*3)
	for index := range logicals {
		register.Add(1)
		go func(logical LogicalSend) {
			defer register.Done()
			if err := verifier.RegisterSend(logical, time.Unix(6_000, 0)); err != nil {
				errs <- err
			}
		}(logicals[index])
	}
	register.Wait()

	stopSnapshots := make(chan struct{})
	var snapshots sync.WaitGroup
	snapshots.Add(1)
	go func() {
		defer snapshots.Done()
		for {
			select {
			case <-stopSnapshots:
				return
			default:
				_ = verifier.Snapshot()
			}
		}
	}()

	acker := discardRecvAcker{}
	var complete sync.WaitGroup
	for index := range logicals {
		complete.Add(1)
		go func(index int, logical LogicalSend, recv *frame.RecvPacket) {
			defer complete.Done()
			ack := &frame.SendackPacket{MessageID: int64(index + 1), MessageSeq: 1, ClientMsgNo: logical.ClientMsgNo, ReasonCode: frame.ReasonSuccess}
			if index%2 == 0 {
				if err := verifier.HandleSendack(ack); err != nil {
					errs <- err
				}
				if err := verifier.HandleRecv(context.Background(), logical.Target, recv, acker); err != nil {
					errs <- err
				}
			} else {
				if err := verifier.HandleRecv(context.Background(), logical.Target, recv, acker); err != nil {
					errs <- err
				}
				if err := verifier.HandleSendack(ack); err != nil {
					errs <- err
				}
			}
			if err := verifier.ReleaseSend(logical); err != nil {
				errs <- err
			}
			verifier.ReleaseRecipient(logical.Target)
		}(index, logicals[index], recvs[index])
	}
	complete.Wait()
	close(stopSnapshots)
	snapshots.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent verifier error = %v", err)
	}

	snapshot := verifier.Snapshot()
	if snapshot.Sent != sends || snapshot.Acknowledged != sends || snapshot.Received != sends || snapshot.ReceiveAcknowledged != sends {
		t.Fatalf("concurrent counters = %+v", snapshot)
	}
	if snapshot.PendingCurrent != 0 || snapshot.SequenceCurrent != 0 || snapshot.CorrelationCurrent != 0 || snapshot.DeadlineCurrent != 0 {
		t.Fatalf("concurrent state retained = %+v", snapshot)
	}
	if snapshot.Sampled != 1 || snapshot.SampledDelivered != 1 {
		t.Fatalf("concurrent sampling = %+v", snapshot)
	}
}

func TestVerifierConcurrentHeavyRecvAndSendackUseIndependentStateDomains(t *testing.T) {
	model, verifier := newTestVerifier(t, 256, 256, 16, 5*time.Second)
	const operations = 64
	sends := make([]LogicalSend, operations)
	for index := 0; index < operations; index++ {
		sends[index] = mustLogicalSend(t, model, 0, uint64(10_000+index), TrafficPerson, "send-source-"+strconv.Itoa(index), "send-target-"+strconv.Itoa(index))
		if err := verifier.RegisterSend(sends[index], time.Unix(7_000, 0)); err != nil {
			t.Fatalf("RegisterSend(%d) error = %v", index, err)
		}
	}
	group := mustLogicalSend(t, model, 1, 20_000, TrafficGroup, "group-sender", "group-channel")
	payload, err := model.BuildPayload(group, 16*1_024)
	if err != nil {
		t.Fatalf("BuildPayload(16KiB) error = %v", err)
	}
	recv := &frame.RecvPacket{
		MessageID:   20_000,
		MessageSeq:  1,
		ClientMsgNo: group.ClientMsgNo,
		ChannelID:   group.Target,
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     group.Sender,
		Payload:     payload,
	}

	errs := make(chan error, operations*3)
	var work sync.WaitGroup
	for index := 0; index < operations; index++ {
		work.Add(2)
		go func(index int) {
			defer work.Done()
			ack := &frame.SendackPacket{MessageID: int64(30_000 + index), MessageSeq: uint64(index + 1), ClientMsgNo: sends[index].ClientMsgNo, ReasonCode: frame.ReasonSuccess}
			if err := verifier.HandleSendack(ack); err != nil {
				errs <- err
				return
			}
			if err := verifier.ReleaseSend(sends[index]); err != nil {
				errs <- err
			}
		}(index)
		go func(index int) {
			defer work.Done()
			recipient := "group-member-" + strconv.Itoa(index)
			if err := verifier.HandleRecv(context.Background(), recipient, recv, discardRecvAcker{}); err != nil {
				errs <- err
				return
			}
			verifier.ReleaseRecipient(recipient)
		}(index)
	}
	work.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent heavy verifier error = %v", err)
	}
	snapshot := verifier.Snapshot()
	if snapshot.Acknowledged != operations || snapshot.Received != operations || snapshot.ReceiveAcknowledged != operations {
		t.Fatalf("concurrent heavy counters = %+v", snapshot)
	}
}

func BenchmarkVerifierParallelGroupFanout16KiB(b *testing.B) {
	cfg := FormalConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		b.Fatalf("NewIdentitySpace() error = %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		b.Fatalf("NewTrafficModel() error = %v", err)
	}
	evidence, err := NewEvidenceRecorder(1, 1)
	if err != nil {
		b.Fatalf("NewEvidenceRecorder() error = %v", err)
	}
	verifier, err := NewVerifier(model, VerifierConfig{
		PendingCapacity:     128,
		SequenceCapacity:    128,
		CorrelationCapacity: 8,
		CorrelationDeadline: 5 * time.Second,
	}, evidence)
	if err != nil {
		b.Fatalf("NewVerifier() error = %v", err)
	}
	logical, err := model.NewLogicalSend(0, 30_000, TrafficGroup, "group-sender", "group-channel")
	if err != nil {
		b.Fatalf("NewLogicalSend() error = %v", err)
	}
	payload, err := model.BuildPayload(logical, 16*1_024)
	if err != nil {
		b.Fatalf("BuildPayload() error = %v", err)
	}
	recv := &frame.RecvPacket{
		MessageID:   30_000,
		MessageSeq:  1,
		ClientMsgNo: logical.ClientMsgNo,
		ChannelID:   logical.Target,
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     logical.Sender,
		Payload:     payload,
	}
	var workers atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		recipient := "benchmark-member-" + strconv.FormatUint(workers.Add(1), 10)
		for pb.Next() {
			if err := verifier.HandleRecv(context.Background(), recipient, recv, discardRecvAcker{}); err != nil {
				b.Errorf("HandleRecv() error = %v", err)
				return
			}
			verifier.ReleaseRecipient(recipient)
		}
	})
}

type recordingRecvAcker struct {
	acks []*frame.RecvackPacket
	err  error
}

type scriptedRecvAcker struct {
	mu    sync.Mutex
	calls int
	errs  []error
}

func (a *scriptedRecvAcker) AckRecv(_ context.Context, _ *frame.RecvackPacket) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	index := a.calls
	a.calls++
	if index >= len(a.errs) {
		return nil
	}
	return a.errs[index]
}

type discardRecvAcker struct{}

func (discardRecvAcker) AckRecv(context.Context, *frame.RecvackPacket) error { return nil }

func (a *recordingRecvAcker) AckRecv(_ context.Context, ack *frame.RecvackPacket) error {
	a.acks = append(a.acks, ack)
	return a.err
}

func newTestVerifier(t *testing.T, pending, sequences, correlations int, deadline time.Duration) (TrafficModel, *Verifier) {
	t.Helper()
	model := newTestTrafficModel(t, FormalConfig())
	evidence, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder() error = %v", err)
	}
	verifier, err := NewVerifier(model, VerifierConfig{
		PendingCapacity:     pending,
		SequenceCapacity:    sequences,
		CorrelationCapacity: correlations,
		CorrelationDeadline: deadline,
	}, evidence)
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	return model, verifier
}

func newTestRetryPolicy(t *testing.T, model TrafficModel) RetryPolicy {
	t.Helper()
	policy, err := NewRetryPolicy(model.identity, FormalConfig().Workload.Retry)
	if err != nil {
		t.Fatalf("NewRetryPolicy() error = %v", err)
	}
	return policy
}

func assertVerificationCode(t *testing.T, err error, want FailureCode) {
	t.Helper()
	var verification *VerificationError
	if !errors.As(err, &verification) {
		t.Fatalf("error = %T %v, want VerificationError(%v)", err, err, want)
	}
	if verification.Code() != want {
		t.Fatalf("verification code = %v, want %v", verification.Code(), want)
	}
}

func mustRecvPacket(t *testing.T, model TrafficModel, logical LogicalSend, messageID int64, messageSeq uint64) *frame.RecvPacket {
	t.Helper()
	payload, err := model.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	channelID := logical.Target
	channelType := uint8(frame.ChannelTypeGroup)
	if logical.Kind == TrafficPerson {
		channelID = logical.Sender
		channelType = frame.ChannelTypePerson
	}
	return &frame.RecvPacket{
		MessageID:   messageID,
		MessageSeq:  messageSeq,
		ClientMsgNo: logical.ClientMsgNo,
		ChannelID:   channelID,
		ChannelType: channelType,
		FromUID:     logical.Sender,
		Payload:     payload,
	}
}

func firstSampledLogical(t *testing.T, model TrafficModel, verifier *Verifier, sender, target string, at time.Time) LogicalSend {
	t.Helper()
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		logical := mustLogicalSend(t, model, 0, ordinal, TrafficPerson, sender, target)
		before := verifier.Snapshot().Sampled
		if err := verifier.RegisterSend(logical, at); err != nil {
			t.Fatalf("RegisterSend(%d) error = %v", ordinal, err)
		}
		if verifier.Snapshot().Sampled > before {
			return logical
		}
	}
	t.Fatal("no sampled logical send in one exact cycle")
	return LogicalSend{}
}

func evidenceCountForClass(snapshot EvidenceSnapshot, class FailureClass) uint64 {
	for _, candidate := range snapshot.Classes {
		if candidate.Class == class {
			return candidate.Count
		}
	}
	return 0
}
