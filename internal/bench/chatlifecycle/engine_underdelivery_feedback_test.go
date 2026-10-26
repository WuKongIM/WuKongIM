package chatlifecycle

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// These fake-clock cases exercise real grant admission and engine advancement.
// They verify a diagnostic distinction, not the cause of a historical cloud run.
func TestEngineUnderDeliveryFeedbackDistinguishesPrimaryDispatchExpiry(t *testing.T) {
	fixture, now := newUnderDeliveryFeedbackFixture(t)
	first, err := fixture.engine.ApplyGrant(context.Background(), now, 100)
	if err != nil || !first.Admitted {
		t.Fatalf("first ApplyGrant = %+v, %v", first, err)
	}
	waitForEngineCompletions(t, fixture.engine, "first feedback grant")
	second, err := fixture.engine.ApplyGrant(context.Background(), now, 10)
	if err != nil || !second.Admitted || second.Snapshot.Released != 10 {
		t.Fatalf("second ApplyGrant = %+v, %v", second, err)
	}

	// Every seeded arrival is within the next second. Moving only the fake
	// clock beyond that second and the unchanged 100ms tolerance makes each
	// admitted primary work item late before processAttempt is reached.
	late := now.Add(2 * time.Second)
	fixture.clock.Set(late)
	_, err = fixture.engine.Advance(late)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	assertUnderDeliveryFeedbackJSON(t, fixture.evidence.Snapshot(), FailureCode(37), 10)
}

func TestEngineUnderDeliveryFeedbackDistinguishesEligibleActivityExpiry(t *testing.T) {
	fixture, now := newUnderDeliveryFeedbackFixture(t)
	first, err := fixture.engine.ApplyGrant(context.Background(), now, 0)
	if err != nil || !first.Admitted {
		t.Fatalf("zero first ApplyGrant = %+v, %v", first, err)
	}

	// The sessions remain eligible for an hour. This crosses the existing
	// five-minute activity window without changing production thresholds or
	// expiring the session generation that owns the queued activity.
	expired := now.Add(6 * time.Minute)
	fixture.clock.Set(expired)
	_, err = fixture.engine.ApplyGrant(context.Background(), expired, 100)
	assertRuntimeFailure(t, err, RuntimeFailureUnderDelivery)
	assertUnderDeliveryFeedbackJSON(t, fixture.evidence.Snapshot(), FailureCode(39), 1)
}

func newUnderDeliveryFeedbackFixture(t *testing.T) (engineTestFixture, time.Time) {
	t.Helper()
	fixture := newEngineTestFixture(t, engineTestLimits{
		OnlineUsers: 100, NewUsersPerDay: 250_000, SessionDuration: time.Hour,
		WorkCapacity: 8_192, InflightCapacity: 512, MaxWorkPerAdvance: 8_192,
		ActivityEligibilityWindow: 5 * time.Minute,
	})
	fixture.factory.autoAck = true
	if err := fixture.engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = fixture.engine.Stop() })
	now := fixture.clock.Now().Add(30 * time.Second)
	fixture.clock.Set(now)
	now, _ = fixture.bootstrapScheduledLogins(t, now)
	return fixture, now
}

func assertUnderDeliveryFeedbackJSON(t *testing.T, snapshot EvidenceSnapshot, code FailureCode, count uint64) {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal evidence: %v", err)
	}
	// Pin the append-only code values independently of new production constants
	// so the unchanged source compiles and goes RED for ambiguous code27.
	type example struct {
		Code  uint8  `json:"code"`
		Value uint64 `json:"value"`
	}
	var wire struct {
		Classes []struct {
			Class uint8     `json:"class"`
			Count uint64    `json:"count"`
			First []example `json:"first"`
			Last  []example `json:"last"`
		} `json:"classes"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal evidence: %v", err)
	}
	for _, class := range wire.Classes {
		if class.Class != 4 {
			continue
		}
		if class.Count != count {
			t.Fatalf("under-delivery events = %d, want %d: %s", class.Count, count, encoded)
		}
		examples := append(class.First, class.Last...)
		if len(examples) == 0 {
			t.Fatalf("under-delivery has no retained examples: %s", encoded)
		}
		for _, item := range examples {
			if item.Code != uint8(code) || item.Value != 1 {
				t.Fatalf("under-delivery lacks emitter code %d: %s", code, encoded)
			}
		}
		return
	}
	t.Fatalf("under-delivery class missing: %s", encoded)
}

func feedbackUnderDeliveryCode(code FailureCode) bool {
	return code == 27 || (code >= 37 && code <= 46)
}

func feedbackEvidenceHasUnderDeliveryCode(snapshot EvidenceSnapshot) bool {
	for _, class := range snapshot.Classes {
		for _, side := range [][]EvidenceExample{class.First, class.Last} {
			for _, item := range side {
				if feedbackUnderDeliveryCode(item.Code) {
					return true
				}
			}
		}
	}
	return false
}

func TestEngineUnderDeliveryFeedbackVocabularyIsClosedAndAppendOnly(t *testing.T) {
	names := []string{
		"offered_dispatch_sampled_deadline", "offered_dispatch_fresh_deadline",
		"offered_activity_eligibility", "offered_lifecycle_eligibility",
		"offered_person_route_unavailable", "offered_group_route_unavailable",
		"offered_deferred_group_eligibility", "offered_canary_eligibility",
		"offered_cleanup_deferred_group", "offered_cleanup_pending_activity",
	}
	for offset, name := range names {
		code := FailureCode(37 + offset)
		recorder, err := NewEvidenceRecorder(1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Record(EvidenceEvent{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: code, Value: 1}); err != nil {
			t.Fatalf("append-only emitter code %d rejected: %v", code, err)
		}
		if got := failureCodeName(code); got != name {
			t.Fatalf("code %d name = %q, want %q", code, got, name)
		}
		if err := recorder.Record(EvidenceEvent{Class: FailureClassHarness, Stage: EvidenceStageSend, Code: code, Value: 1}); err == nil {
			t.Fatalf("emitter code %d accepted outside capacity stage", code)
		}
		if snapshot := recorder.Snapshot(); snapshot.Classification != SyncClassificationHarnessInvalid || snapshot.Classes[0].Count != 1 {
			t.Fatalf("emitter code %d changed aggregate verdict: %+v", code, snapshot)
		}
	}
	if failureCodeName(FailureCode(27)) != "offered_load_under_delivery" {
		t.Fatal("historical unknown-emitter code27 was renumbered")
	}
	recorder, _ := NewEvidenceRecorder(1, 1)
	if err := recorder.Record(EvidenceEvent{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCode(47)}); err == nil {
		t.Fatal("unknown future emitter code47 was accepted")
	}
}
