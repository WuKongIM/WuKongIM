package chatlifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// Failure inventory, before implementation: successful hot ACKs above the exact
// P99 limit but below the first deadline have no join key; exact limits, cold or
// retry results are mislabeled; late evidence/native counters change; a prefix
// loses the failure-window tail; retained state grows; duplicate/unknown clocks
// invent anchors; reset or detached snapshots cross generation ownership. Only
// existing SDK clocks and opaque attempt keys may enter this optional evidence.
type slowHotSuccessWireRecord struct {
	AttemptLinkRecord
	ObservedAt time.Time `json:"observed_at"`
}

type slowHotSuccessWire struct {
	LimitNanos  time.Duration              `json:"limit_nanos"`
	Seen        uint64                     `json:"seen"`
	Overwritten uint64                     `json:"overwritten"`
	Records     []slowHotSuccessWireRecord `json:"records"`
}

func slowHotSuccessFixture(t *testing.T, enabled bool) (TrafficModel, *Verifier) {
	t.Helper()
	model := newTestTrafficModel(t, FormalConfig())
	evidence, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(model, VerifierConfig{
		PendingCapacity: 64, SequenceCapacity: 64, CorrelationCapacity: 64, CorrelationDeadline: 10 * time.Second,
		EnableAttemptLinkDiagnostics: enabled, HotLatency: LatencyLimit{P99: 600 * time.Millisecond, P999: time.Second},
	}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	return model, verifier
}

func slowHotSuccessCut(t *testing.T, v *Verifier, generation uint64) *slowHotSuccessWire {
	t.Helper()
	diagnostics := v.AttemptLinkDiagnostics(generation)
	if diagnostics == nil {
		return nil
	}
	body, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Slow *slowHotSuccessWire `json:"slow_hot_success"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Slow == nil {
		t.Fatal("confirmed timely hot successes have no bounded attempt-link evidence capability")
	}
	return wire.Slow
}

func slowHotSuccessACK(t *testing.T, model TrafficModel, v *Verifier, index uint64, at time.Time, duration time.Duration, class SendLatencyClass) LogicalSend {
	t.Helper()
	logical := mustLogicalSend(t, model, 0, index, TrafficPerson, "private-slow-sender", "private-slow-recipient")
	if err := v.RegisterSend(logical, at, class); err != nil {
		t.Fatal(err)
	}
	attempt, err := newTestRetryPolicy(t, model).Attempt(logical, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.ObserveAttemptWithDeadlineEvidence(logical, attempt, index, at.Add(2*time.Second), AttemptLinkRegistration{ConnectFingerprint: [16]byte{7}}); err != nil {
		t.Fatal(err)
	}
	completed := at.Add(duration)
	if duration == -time.Second {
		completed = time.Time{} // An unknown SDK clock cannot create an anchor.
	}
	if err := v.HandleSendackWithEvidenceAt(firstDeadlineAck(logical, index), completed, AttemptLinkResultEvidence{
		ConnectFingerprint: [16]byte{7}, Kind: "sendack_success", PendingStartedAt: at, WriteStartedAt: at.Add(time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	if err := v.ReleaseSend(logical); err != nil {
		t.Fatal(err)
	}
	return logical
}

func TestSlowHotSuccessSelectsExactConfiguredTimelyACKWithoutChangingNative(t *testing.T) {
	for _, test := range []struct {
		name     string
		duration time.Duration
		class    SendLatencyClass
		want     uint64
	}{
		{"below", 599 * time.Millisecond, SendLatencyHot, 0},
		{"equal", 600 * time.Millisecond, SendLatencyHot, 0},
		{"one_ns_above", 600*time.Millisecond + 1, SendLatencyHot, 1},
		{"tail_limit_equal", time.Second, SendLatencyHot, 1},
		{"tail_above", time.Second + 1, SendLatencyHot, 1},
		{"deadline_equal_stays_late", 2 * time.Second, SendLatencyHot, 0},
		{"deadline_above_stays_late", 2*time.Second + 1, SendLatencyHot, 0},
		{"cold_not_hot", time.Second, SendLatencyColdFirstCreate, 0},
		{"negative_clock", -time.Nanosecond, SendLatencyHot, 0},
		{"unknown_clock", -time.Second, SendLatencyHot, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var native [2]VerifierSnapshot
			for i, enabled := range []bool{false, true} {
				model, v := slowHotSuccessFixture(t, enabled)
				at := time.Unix(123456, 0)
				logical := slowHotSuccessACK(t, model, v, 1, at, test.duration, test.class)
				native[i] = v.Snapshot()
				got := slowHotSuccessCut(t, v, 1)
				if !enabled {
					if got != nil {
						t.Fatal("disabled evidence retained state")
					}
					continue
				}
				if got.LimitNanos != 600*time.Millisecond || got.Seen != test.want || len(got.Records) != int(test.want) {
					t.Fatalf("slow success=%+v want%d", got, test.want)
				}
				if test.want == 1 {
					r := got.Records[0]
					if r.Key != (sendtrace.AttemptKey{MessageFingerprint: sendtrace.MessageFingerprint(logical.ClientMsgNo), ClientSeq: 1, ConnectFingerprint: [16]byte{7}}) ||
						r.Kind != "sendack_success" || r.Attribution != "candidate" || !r.ObservedAt.Equal(at.Add(test.duration)) ||
						r.ObservedAfterRegistration != test.duration || r.DeadlineAfterRegistration != 2*time.Second || r.TimingMask != 3 {
						t.Fatalf("exact timely ACK identity/clock lost: %+v", r)
					}
				}
				wantLate := 0
				if test.duration >= 2*time.Second {
					wantLate = 1
				}
				if legacy := v.AttemptLinkDiagnostics(1); len(legacy.Records) != wantLate {
					t.Fatalf("original late prefix changed: %+v", legacy)
				}
			}
			if !reflect.DeepEqual(native[0], native[1]) {
				t.Fatal("optional sampling changed first deadlines, latency or correctness")
			}
		})
	}
}

func TestSlowHotSuccessRetainsLatestSixteenDetachedAndResets(t *testing.T) {
	model, v := slowHotSuccessFixture(t, true)
	at := time.Unix(123456, 0)
	for i := uint64(1); i <= 20; i++ {
		slowHotSuccessACK(t, model, v, i, at.Add(time.Duration(i)*time.Second), 700*time.Millisecond, SendLatencyHot)
	}
	got := slowHotSuccessCut(t, v, 1)
	if got.Seen != 20 || got.Overwritten != 4 || len(got.Records) != 16 || got.Records[0].Ordinal != 5 || got.Records[15].Ordinal != 20 {
		t.Fatalf("latest bounded failure-window anchors=%+v", got)
	}
	got.Records[0].Key.ClientSeq = 999
	if next := slowHotSuccessCut(t, v, 1); next.Records[0].Key.ClientSeq != 5 {
		t.Fatal("snapshot alias changed recorder")
	}
	if len(v.AttemptLinkDiagnostics(1).Records) != 0 {
		t.Fatal("timely anchors entered confirmed-late prefix")
	}
	v.resetRuntime()
	reset := slowHotSuccessCut(t, v, 2)
	if reset.LimitNanos != 600*time.Millisecond || reset.Seen != 0 || reset.Overwritten != 0 || len(reset.Records) != 0 {
		t.Fatalf("new generation inherited prior sample or lost exact limit: %+v", reset)
	}
}

func TestSlowHotSuccessIgnoresRetryAndPreviouslyObservedOriginal(t *testing.T) {
	for _, previous := range []string{"retry", "earlier_transport_error", "duplicate_ack"} {
		t.Run(previous, func(t *testing.T) {
			model, v := slowHotSuccessFixture(t, true)
			at := time.Unix(123456, 0)
			logical := mustLogicalSend(t, model, 0, 1, TrafficPerson, "private-slow-sender", "private-slow-recipient")
			if err := v.RegisterSend(logical, at, SendLatencyHot); err != nil {
				t.Fatal(err)
			}
			policy := newTestRetryPolicy(t, model)
			first, _ := policy.Attempt(logical, 0)
			if err := v.ObserveAttemptWithDeadlineEvidence(logical, first, 1, at.Add(2*time.Second), AttemptLinkRegistration{ConnectFingerprint: [16]byte{7}}); err != nil {
				t.Fatal(err)
			}
			seq := uint64(1)
			switch previous {
			case "retry":
				retry, _ := policy.Attempt(logical, 1)
				if err := v.ObserveAttemptWithDeadlineEvidence(logical, retry, 2, at.Add(2*time.Second), AttemptLinkRegistration{ConnectFingerprint: [16]byte{7}}); err != nil {
					t.Fatal(err)
				}
				seq = 2
			case "earlier_transport_error":
				v.ObserveAttemptErrorWithEvidenceAt(logical.ClientMsgNo, 1, at.Add(100*time.Millisecond), AttemptLinkResultEvidence{Kind: "transport_error"})
			}
			ack := firstDeadlineAck(logical, seq)
			evidence := AttemptLinkResultEvidence{Kind: "sendack_success", ConnectFingerprint: [16]byte{7}}
			if err := v.HandleSendackWithEvidenceAt(ack, at.Add(700*time.Millisecond), evidence); err != nil {
				t.Fatal(err)
			}
			want := uint64(0)
			if previous == "duplicate_ack" {
				want = 1
				_ = v.HandleSendackWithEvidenceAt(ack, at.Add(701*time.Millisecond), evidence)
			}
			if got := slowHotSuccessCut(t, v, 1); got.Seen != want {
				t.Fatalf("non-original/duplicate anchor fabricated: %+v", got)
			}
		})
	}
}

func TestSlowHotSuccessMalformedOptionalWireStaysInvalid(t *testing.T) {
	at := time.Unix(123456, 0).UTC()
	good := slowHotSuccessWire{
		LimitNanos: 600 * time.Millisecond, Seen: 1,
		Records: []slowHotSuccessWireRecord{{AttemptLinkRecord: AttemptLinkRecord{
			Ordinal: 1, Key: sendtrace.AttemptKey{MessageFingerprint: [16]byte{1}, ClientSeq: 1, ConnectFingerprint: [16]byte{7}},
			Kind: "sendack_success", Attribution: "candidate", DeadlineAfterRegistration: 2 * time.Second,
			ObservedAfterRegistration: 700 * time.Millisecond, SDKWriteToObserved: 700 * time.Millisecond,
		}, ObservedAt: at}},
	}
	for _, test := range []struct {
		name string
		bad  func(*slowHotSuccessWire)
	}{
		{"valid", nil},
		{"zero_limit", func(s *slowHotSuccessWire) { s.LimitNanos = 0 }},
		{"negative_limit", func(s *slowHotSuccessWire) { s.LimitNanos = -1 }},
		{"count_mismatch", func(s *slowHotSuccessWire) { s.Seen = 0 }},
		{"overwrite_mismatch", func(s *slowHotSuccessWire) { s.Overwritten = 1 }},
		{"capacity", func(s *slowHotSuccessWire) {
			s.Records = append(s.Records, make([]slowHotSuccessWireRecord, 16)...)
			s.Seen = 17
		}},
		{"zero_ordinal", func(s *slowHotSuccessWire) { s.Records[0].Ordinal = 0 }},
		{"last_ordinal_mismatch", func(s *slowHotSuccessWire) { s.Records[0].Ordinal = 2 }},
		{"equal_limit", func(s *slowHotSuccessWire) { s.Records[0].ObservedAfterRegistration = s.LimitNanos }},
		{"deadline_equal", func(s *slowHotSuccessWire) { s.Records[0].ObservedAfterRegistration = 2 * time.Second }},
		{"unknown_clock", func(s *slowHotSuccessWire) { s.Records[0].ObservedAt = time.Time{} }},
		{"not_success", func(s *slowHotSuccessWire) { s.Records[0].Kind = "transport_error" }},
		{"bad_candidate", func(s *slowHotSuccessWire) { s.Records[0].Key.ConnectFingerprint = [16]byte{} }},
		{"negative_segment", func(s *slowHotSuccessWire) { s.Records[0].SDKWriteToObserved = -1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := good
			copy.Records = append([]slowHotSuccessWireRecord(nil), good.Records...)
			if test.bad != nil {
				test.bad(&copy)
			}
			body, err := json.Marshal(map[string]any{"generation": uint64(1), "dropped": 0, "records": []AttemptLinkRecord{}, "slow_hot_success": copy})
			if err != nil {
				t.Fatal(err)
			}
			got := decodeAttemptLinkDiagnostics(body, 1)
			if got == nil {
				t.Fatal("new child erased original late-prefix capability")
			}
			body, _ = json.Marshal(got)
			var decoded struct {
				Slow *slowHotSuccessWire `json:"slow_hot_success"`
			}
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			if (decoded.Slow != nil) != (test.bad == nil) {
				t.Fatalf("new child validity=%t want%t", decoded.Slow != nil, test.bad == nil)
			}
		})
	}
}

func TestSlowHotSuccessMaximumOrdinalWireAndUnknownConnectionStayExplicit(t *testing.T) {
	const maximum = ^uint64(0)
	s := slowHotSuccessWire{LimitNanos: 600 * time.Millisecond, Seen: maximum, Overwritten: maximum - 16}
	for i := uint64(0); i < 16; i++ {
		s.Records = append(s.Records, slowHotSuccessWireRecord{AttemptLinkRecord: AttemptLinkRecord{
			Ordinal: maximum - 15 + i, Key: sendtrace.AttemptKey{MessageFingerprint: [16]byte{1}, ClientSeq: uint32(i + 1)},
			Kind: "sendack_success", Attribution: "connection_unknown", DeadlineAfterRegistration: 2 * time.Second, ObservedAfterRegistration: 700 * time.Millisecond,
		}, ObservedAt: time.Unix(123456, int64(i)).UTC()})
	}
	body, err := json.Marshal(map[string]any{"generation": uint64(1), "dropped": 0, "records": []AttemptLinkRecord{}, "slow_hot_success": s})
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeAttemptLinkDiagnostics(body, 1); got == nil {
		t.Fatal("bounded maximum counter or explicitly unknown connection discarded")
	}
}

func TestSlowHotSuccessRequiresSameSourceWorkerProtocol(t *testing.T) {
	if workerProtocolVersion != 19 {
		t.Fatalf("new optional evidence must isolate old live peers: protocol=%d want19", workerProtocolVersion)
	}
}

func TestSlowHotSuccessFullRetainedShapeFitsExistingSidecarBudget(t *testing.T) {
	cfg := LocalConfig()
	snapshots := coordinatorSnapshotFixture(WorkerFence{RunID: cfg.RunID, AssignmentID: "slow-hot-budget", Generation: 1}, 1, time.Minute, 1)
	for workerID := range snapshots {
		model, v := slowHotSuccessFixture(t, true)
		at := time.Unix(123456, 0)
		for i := uint64(1); i <= 20; i++ {
			slowHotSuccessACK(t, model, v, i, at.Add(time.Duration(i)*time.Second), 700*time.Millisecond, SendLatencyHot)
		}
		for i := uint64(21); i <= 36; i++ {
			slowHotSuccessACK(t, model, v, i, at.Add(time.Duration(i)*time.Second), 2*time.Second, SendLatencyHot)
		}
		if got := slowHotSuccessCut(t, v, 1); len(got.Records) != 16 || len(v.AttemptLinkDiagnostics(1).Records) != 16 {
			t.Fatal("budget fixture missing one complete retained set")
		}
		snapshots[workerID].AttemptLinkDiagnostics = v.AttemptLinkDiagnostics(1)
		snapshots[workerID].Phase = WorkerPhaseFinal
	}
	dir := t.TempDir()
	r := newLiveDiagnosticRecorder(dir, cfg.RunID, time.Unix(123456, 0), nil, LatencyLimit{})
	if err := r.writeAttemptLinkDiagnostics(time.Unix(123500, 0), snapshots, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "attempt-link-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 64<<10 || !strings.Contains(string(body), "wukongim/chat-lifecycle-attempt-link-diagnostics/v2") || strings.Contains(string(body), "private-slow") || strings.Contains(string(body), "client_msg_no") {
		t.Fatalf("new diagnostics exceeded existing cap, lost version isolation or exposed raw identity: bytes=%d", len(body))
	}
	if err := r.writeTerminalWorkerEvidence(time.Unix(123500, 0), snapshots); err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(filepath.Join(dir, "worker-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(native), "slow_hot_success") || strings.Contains(string(native), "attempt_link_diagnostics") {
		t.Fatal("optional sample history entered native verdict evidence")
	}
}

// The ordinary-value full-set regression does not cover JSON's widest accepted
// integers, timestamps and escaped assignment identities. Keep both bounds.
func TestSlowHotSuccessMaximumWidthFitsExisting64KiBCap(t *testing.T) {
	const maximum = ^uint64(0)
	var key sendtrace.AttemptKey
	for i := range key.MessageFingerprint {
		key.MessageFingerprint[i], key.ConnectFingerprint[i] = 255, 255
	}
	key.ClientSeq = ^uint32(0)
	deadline := time.Duration(1<<63 - 1)
	d := &AttemptLinkDiagnosticsSnapshot{Generation: maximum, Dropped: maximum,
		SlowHotSuccess: &SlowHotSuccessDiagnosticsSnapshot{LimitNanos: deadline - 2, Seen: maximum, Overwritten: maximum - 16}}
	for i := uint64(0); i < 16; i++ {
		r := AttemptLinkRecord{Ordinal: i + 1, Key: key, Kind: "sendack_rejected", Attribution: "connection_unknown",
			DeadlineAfterRegistration: deadline, ObservedAfterRegistration: deadline, SDKPendingToWrite: deadline, SDKWriteToObserved: deadline, TimingMask: 3}
		d.Records = append(d.Records, r)
		r.Ordinal, r.Kind, r.ObservedAfterRegistration = maximum-15+i, "sendack_success", deadline-1
		d.SlowHotSuccess.Records = append(d.SlowHotSuccess.Records, SlowHotSuccessRecord{AttemptLinkRecord: r,
			ObservedAt: time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("maximum-offset", 23*3600+59*60))})
	}
	if !validAttemptLinkDiagnostics(d, maximum) || !validSlowHotSuccess(d.SlowHotSuccess) {
		t.Fatal("maximum-width fixture must be an accepted optional wire shape")
	}
	cfg := LocalConfig()
	cfg.RunID = strings.Repeat("\x01", 128)
	snapshots := make([]WorkerSnapshot, 3)
	for i := range snapshots {
		snapshots[i] = WorkerSnapshot{RunID: cfg.RunID, AssignmentID: cfg.RunID, Generation: maximum, WorkerID: uint64(i),
			WorkerCount: 3, SnapshotSequence: maximum, Uptime: deadline, Phase: WorkerPhaseFinal, AttemptLinkDiagnostics: d}
	}
	dir := t.TempDir()
	r := newLiveDiagnosticRecorder(dir, cfg.RunID, time.Unix(123456, 0), nil, LatencyLimit{})
	if err := r.writeAttemptLinkDiagnostics(d.SlowHotSuccess.Records[0].ObservedAt, snapshots, false); err != nil {
		t.Fatalf("accepted full-width evidence does not fit unchanged64KiB cap: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "attempt-link-diagnostics.json"))
	if err != nil || len(body) > 64<<10 {
		t.Fatalf("maximum evidence bytes=%d err=%v", len(body), err)
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil || len(doc) != 4 {
		t.Fatal("bounded evidence lost complete document")
	}
}
