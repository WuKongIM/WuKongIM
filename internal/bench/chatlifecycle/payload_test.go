package chatlifecycle

import (
	"bytes"
	"errors"
	"testing"
)

func TestTrafficAndPayloadDistributionsAreExactStableCycles(t *testing.T) {
	model := newTestTrafficModel(t, FormalConfig())
	trafficCounts := map[TrafficKind]int{}
	payloadCounts := map[int]int{}
	for ordinal := uint64(0); ordinal < 10_000; ordinal++ {
		kind, err := model.TrafficFor(ordinal)
		if err != nil {
			t.Fatalf("TrafficFor(%d) error = %v", ordinal, err)
		}
		trafficCounts[kind]++
		size, err := model.PayloadSizeFor(ordinal)
		if err != nil {
			t.Fatalf("PayloadSizeFor(%d) error = %v", ordinal, err)
		}
		payloadCounts[size]++
	}
	if trafficCounts[TrafficPerson] != 9_000 || trafficCounts[TrafficGroup] != 1_000 {
		t.Fatalf("traffic counts = %v, want 9000/1000", trafficCounts)
	}
	wantPayloads := map[int]int{256: 7_000, 1_024: 2_500, 4_096: 400, 16_384: 100}
	for size, want := range wantPayloads {
		if got := payloadCounts[size]; got != want {
			t.Fatalf("payload %d count = %d, want %d (all=%v)", size, got, want, payloadCounts)
		}
	}
}

func TestDirectionDistributionAndSenderSemantics(t *testing.T) {
	model := newTestTrafficModel(t, FormalConfig())
	counts := map[PersonDirection]int{}
	for channel := uint64(0); channel < 10_000; channel++ {
		direction, err := model.DirectionFor(channel)
		if err != nil {
			t.Fatalf("DirectionFor(%d) error = %v", channel, err)
		}
		counts[direction]++
	}
	if counts[DirectionAlternating] != 7_000 || counts[DirectionOneWay] != 3_000 {
		t.Fatalf("direction counts = %v, want 7000/3000", counts)
	}

	if sender, err := SenderFor(DirectionAlternating, 0, "lower", "higher"); err != nil || sender != "lower" {
		t.Fatalf("alternating even sender = %q, %v; want lower", sender, err)
	}
	if sender, err := SenderFor(DirectionAlternating, 1, "lower", "higher"); err != nil || sender != "higher" {
		t.Fatalf("alternating odd sender = %q, %v; want higher", sender, err)
	}
	if sender, err := SenderFor(DirectionOneWay, 99, "lower", "higher"); err != nil || sender != "lower" {
		t.Fatalf("one-way sender = %q, %v; want lower", sender, err)
	}
	if _, err := SenderFor(DirectionOneWay, 0, "same", "same"); !errors.Is(err, errDirectionEndpoints) {
		t.Fatalf("SenderFor(equal endpoints) error = %v, want %v", err, errDirectionEndpoints)
	}
}

func TestPayloadMarkerIsCompactVersionedAndAttemptIndependent(t *testing.T) {
	cfg := FormalConfig()
	cfg.RunID = "run-secret-looking-value"
	model := newTestTrafficModel(t, cfg)
	logical, err := model.NewLogicalSend(2, 42, TrafficPerson, "sender-a", "receiver-b")
	if err != nil {
		t.Fatalf("NewLogicalSend() error = %v", err)
	}
	payload, err := model.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	if len(payload) != 256 {
		t.Fatalf("payload length = %d, want 256", len(payload))
	}
	if bytes.Contains(payload, []byte(cfg.RunID)) || bytes.Contains(payload, []byte("sender-a")) || bytes.Contains(payload, []byte("receiver-b")) {
		t.Fatalf("payload leaks raw run or endpoint identity: %q", payload)
	}
	if err := model.VerifyPayload(payload, logical); err != nil {
		t.Fatalf("VerifyPayload() error = %v", err)
	}
	decoded, err := DecodePayloadMarker(payload)
	if err != nil {
		t.Fatalf("DecodePayloadMarker() error = %v", err)
	}
	if decoded.Version != payloadMarkerVersion || decoded.LogicalSend != 42 || decoded.WorkerID != 2 || decoded.Kind != TrafficPerson || decoded.PayloadBytes != 256 {
		t.Fatalf("decoded marker = %+v", decoded)
	}
	if payloadMarkerBytes != 104 || len(decoded.RunFingerprint) != 16 || len(decoded.SenderFingerprint) != 16 || len(decoded.TargetFingerprint) != 16 {
		t.Fatalf("marker layout = %d bytes with fingerprints %d/%d/%d, want 104 and 16/16/16", payloadMarkerBytes, len(decoded.RunFingerprint), len(decoded.SenderFingerprint), len(decoded.TargetFingerprint))
	}

	again, err := model.NewLogicalSend(2, 42, TrafficPerson, "sender-a", "receiver-b")
	if err != nil {
		t.Fatalf("NewLogicalSend() again error = %v", err)
	}
	if logical.ClientMsgNo != again.ClientMsgNo {
		t.Fatalf("logical identity changed: %q != %q", logical.ClientMsgNo, again.ClientMsgNo)
	}
	if logical.ClientMsgNo == "" || bytes.Contains([]byte(logical.ClientMsgNo), []byte(cfg.RunID)) {
		t.Fatalf("client_msg_no is empty or leaks raw run ID: %q", logical.ClientMsgNo)
	}
}

func TestPayloadMarkerStrictlyRejectsCorruptionAndWrongDeclaration(t *testing.T) {
	model := newTestTrafficModel(t, FormalConfig())
	logical, err := model.NewLogicalSend(1, 7, TrafficGroup, "sender", "group")
	if err != nil {
		t.Fatalf("NewLogicalSend() error = %v", err)
	}
	payload, err := model.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func([]byte) []byte
		want   error
	}{
		{name: "short", mutate: func(p []byte) []byte { return p[:payloadMarkerBytes-1] }, want: errPayloadLength},
		{name: "trailing", mutate: func(p []byte) []byte { return append(p, 0) }, want: errPayloadLength},
		{name: "version", mutate: func(p []byte) []byte { p[4]++; return p }, want: errPayloadVersion},
		{name: "reserved flags", mutate: func(p []byte) []byte { p[6] = 1; return p }, want: errPayloadReserved},
		{name: "declared length", mutate: func(p []byte) []byte { p[11]--; return p }, want: errPayloadLength},
		{name: "identity", mutate: func(p []byte) []byte { p[40] ^= 1; return p }, want: errPayloadChecksum},
		{name: "checksum", mutate: func(p []byte) []byte { p[88] ^= 1; return p }, want: errPayloadChecksum},
		{name: "padding", mutate: func(p []byte) []byte { p[len(p)-1] ^= 1; return p }, want: errPayloadPadding},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := append([]byte(nil), payload...)
			candidate = tt.mutate(candidate)
			if _, err := DecodePayloadMarker(candidate); !errors.Is(err, tt.want) {
				t.Fatalf("DecodePayloadMarker() error = %v, want %v", err, tt.want)
			}
		})
	}

	wrongRunConfig := FormalConfig()
	wrongRunConfig.RunID = "another-run"
	wrongRunModel := newTestTrafficModel(t, wrongRunConfig)
	wrongRunLogical, err := wrongRunModel.NewLogicalSend(1, 7, TrafficGroup, "sender", "group")
	if err != nil {
		t.Fatalf("wrong-run NewLogicalSend() error = %v", err)
	}
	declarations := []struct {
		name    string
		model   TrafficModel
		logical LogicalSend
	}{
		{name: "run", model: wrongRunModel, logical: wrongRunLogical},
		{name: "worker", model: model, logical: mustLogicalSend(t, model, 2, 7, TrafficGroup, "sender", "group")},
		{name: "logical send", model: model, logical: mustLogicalSend(t, model, 1, 8, TrafficGroup, "sender", "group")},
		{name: "kind", model: model, logical: mustLogicalSend(t, model, 1, 7, TrafficPerson, "sender", "group")},
		{name: "sender", model: model, logical: mustLogicalSend(t, model, 1, 7, TrafficGroup, "another-sender", "group")},
		{name: "target", model: model, logical: mustLogicalSend(t, model, 1, 7, TrafficGroup, "sender", "another-group")},
	}
	for _, declaration := range declarations {
		t.Run("wrong declaration "+declaration.name, func(t *testing.T) {
			if err := declaration.model.VerifyPayload(payload, declaration.logical); !errors.Is(err, errPayloadDeclaration) {
				t.Fatalf("VerifyPayload() error = %v, want %v", err, errPayloadDeclaration)
			}
		})
	}
}

func TestPayloadMarkerRejectsInvalidOrUnboundedInputs(t *testing.T) {
	model := newTestTrafficModel(t, FormalConfig())
	if _, err := model.NewLogicalSend(3, 0, TrafficPerson, "sender", "target"); !errors.Is(err, errPayloadWorker) {
		t.Fatalf("NewLogicalSend(worker) error = %v, want %v", err, errPayloadWorker)
	}
	if _, err := model.NewLogicalSend(0, 0, TrafficKind(99), "sender", "target"); !errors.Is(err, errTrafficKind) {
		t.Fatalf("NewLogicalSend(kind) error = %v, want %v", err, errTrafficKind)
	}
	if _, err := model.NewLogicalSend(0, 0, TrafficPerson, "", "target"); !errors.Is(err, errPayloadIdentity) {
		t.Fatalf("NewLogicalSend(empty sender) error = %v, want %v", err, errPayloadIdentity)
	}
	logical, err := model.NewLogicalSend(0, 0, TrafficPerson, "sender", "target")
	if err != nil {
		t.Fatalf("NewLogicalSend() error = %v", err)
	}
	if _, err := model.BuildPayload(logical, payloadMarkerBytes-1); !errors.Is(err, errPayloadSize) {
		t.Fatalf("BuildPayload(short) error = %v, want %v", err, errPayloadSize)
	}
	if _, err := model.BuildPayload(logical, maxPayloadBytes+1); !errors.Is(err, errPayloadSize) {
		t.Fatalf("BuildPayload(large) error = %v, want %v", err, errPayloadSize)
	}
}

func TestPayloadMarkerFitsMinimumAndMaximumPayloadClasses(t *testing.T) {
	model := newTestTrafficModel(t, FormalConfig())
	logical := mustLogicalSend(t, model, 0, 99, TrafficPerson, "sender", "target")
	for _, size := range []int{payloadMarkerBytes, 256, 16 * 1_024} {
		payload, err := model.BuildPayload(logical, size)
		if err != nil {
			t.Fatalf("BuildPayload(%d) error = %v", size, err)
		}
		if len(payload) != size {
			t.Fatalf("BuildPayload(%d) length = %d", size, len(payload))
		}
		if err := model.VerifyPayload(payload, logical); err != nil {
			t.Fatalf("VerifyPayload(%d) error = %v", size, err)
		}
	}
}

func TestTrafficModelCopiesPayloadConfiguration(t *testing.T) {
	cfg := FormalConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel() error = %v", err)
	}
	cfg.Workload.Payloads[0] = PayloadShare{Percent: 100, Bytes: 999}
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		size, err := model.PayloadSizeFor(ordinal)
		if err != nil {
			t.Fatalf("PayloadSizeFor(%d) error = %v", ordinal, err)
		}
		if size == 999 {
			t.Fatalf("PayloadSizeFor(%d) observed caller mutation", ordinal)
		}
	}
}

func mustLogicalSend(t *testing.T, model TrafficModel, workerID, logicalOrdinal uint64, kind TrafficKind, sender, target string) LogicalSend {
	t.Helper()
	logical, err := model.NewLogicalSend(workerID, logicalOrdinal, kind, sender, target)
	if err != nil {
		t.Fatalf("NewLogicalSend() error = %v", err)
	}
	return logical
}

func newTestTrafficModel(t *testing.T, cfg Config) TrafficModel {
	t.Helper()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel() error = %v", err)
	}
	return model
}
