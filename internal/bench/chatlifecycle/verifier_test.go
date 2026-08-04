package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
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
		if err := verifier.ObserveAttempt(logical, attempt); err != nil {
			t.Fatalf("ObserveAttempt(%d) error = %v", number, err)
		}
	}
	ack := &frame.SendackPacket{
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
	assertVerificationCode(t, verifier.ObserveAttempt(logical, tampered), FailureCodeGeneratorInvariant)

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
	if snapshot.DuplicateDeliveries != 1 || snapshot.SequenceRegressions != 1 {
		t.Fatalf("delivery counters = %+v", snapshot)
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
	assertVerificationCode(t, err, FailureCodeRecvack)
	if strings.Contains(err.Error(), rawTransportError) {
		t.Fatalf("recvack error leaks transport error: %q", err)
	}
	snapshot := verifier.Snapshot()
	if snapshot.Received != 1 || snapshot.ReceiveAckFailures != 1 || snapshot.ReceiveAcknowledged != 0 {
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
	if after.SampledExpired != 2 || after.CorrelationCurrent != 0 || after.DeadlineCurrent != 0 {
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

type recordingRecvAcker struct {
	acks []*frame.RecvackPacket
	err  error
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
