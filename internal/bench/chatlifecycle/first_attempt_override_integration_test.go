//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The existing injected transport/clock fixture drives the real worker factory,
// Engine and verifier. Unknown JSON compiles on the baseline and fails behaviorally.
func firstFiveConfig(t *testing.T, cfg Config, budget time.Duration) Config {
	t.Helper()
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	body = firstFiveJSON(t, body, budget)
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func firstFiveJSON(t *testing.T, body []byte, budget time.Duration) []byte {
	t.Helper()
	var root, thresholds map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root["thresholds"], &thresholds); err != nil {
		t.Fatal(err)
	}
	latency := hotAttemptBudgetLatencyJSON(t, body)
	latency["first_attempt_timeout"], _ = json.Marshal(budget)
	thresholds["latency"], _ = json.Marshal(latency)
	root["thresholds"], _ = json.Marshal(thresholds)
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFirstFiveEveryPhaseAndClass(t *testing.T) {
	for _, phase := range []time.Duration{0, 600 * time.Second, 4200 * time.Second} {
		for _, class := range []string{"hot", "cold", "reheat"} {
			t.Run(phase.String()+"/"+class, func(t *testing.T) {
				f := hotAttemptBudgetFactoryFixture(t, firstFiveConfig(t, pairedLocalP95Fixture(), 5*time.Second))
				f.clock.Set(f.clock.Now().Add(phase))
				uid := f.identity.UID(0)
				if _, err := f.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 0}); err != nil {
					t.Fatal(err)
				}
				intent := f.intent(t, uid, "first-five-group", 180, TrafficGroup)
				if class == "cold" {
					intent.ColdAttempt = true
				}
				if class == "reheat" {
					intent.Domain = LogicalDomainRevisit
				}
				started := f.clock.Now()
				if err := f.engine.SubmitGranted(intent, started); err != nil {
					t.Fatal(err)
				}
				if _, err := f.engine.Advance(started); err != nil {
					t.Fatal(err)
				}
				waitForEngineAdmissions(t, f.engine)
				if f.factory.sentCount() != 1 {
					t.Fatal("initial SEND setup failed")
				}
				for _, elapsed := range []time.Duration{2 * time.Second, 5*time.Second - time.Nanosecond} {
					f.clock.Set(started.Add(elapsed))
					if _, err := f.engine.Advance(f.clock.Now()); err != nil {
						t.Fatal(err)
					}
					s, err := f.engine.Snapshot()
					if err != nil {
						t.Fatal(err)
					}
					if s.RetryQueueDepth != 0 || s.RetryAttempts != 0 || s.InflightCurrent != 1 || f.factory.sentCount() != 1 {
						t.Fatalf("first attempt retried before5s: elapsed=%s retry_queue=%d retry=%d inflight=%d packets=%d", elapsed, s.RetryQueueDepth, s.RetryAttempts, s.InflightCurrent, f.factory.sentCount())
					}
				}
				f.clock.Set(started.Add(5 * time.Second))
				if _, err := f.engine.Advance(f.clock.Now()); err != nil {
					t.Fatal(err)
				}
				s, err := f.engine.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if s.RetryQueueDepth != 1 || s.RetryAttempts != 0 || s.InflightCurrent != 1 {
					t.Fatal("exact5s did not schedule one retry")
				}
			})
		}
	}
}

func TestFirstFiveRetriesKeepOriginalBudgets(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "hot", true: "cold"}[cold], func(t *testing.T) {
			f := hotAttemptBudgetFactoryFixture(t, firstFiveConfig(t, pairedLocalP95Fixture(), 5*time.Second))
			uid := f.identity.UID(0)
			if _, err := f.engine.Login(context.Background(), SessionLogin{UID: uid, UserIndex: 0}); err != nil {
				t.Fatal(err)
			}
			intent := f.intent(t, uid, "first-five-group", 181, TrafficGroup)
			intent.ColdAttempt = cold
			now := f.clock.Now()
			if err := f.engine.SubmitGranted(intent, now); err != nil {
				t.Fatal(err)
			}
			if _, err := f.engine.Advance(now); err != nil {
				t.Fatal(err)
			}
			waitForEngineAdmissions(t, f.engine)
			for attempt := uint8(0); attempt < 4; attempt++ {
				budget := 2 * time.Second
				if attempt == 0 || cold {
					budget = 5 * time.Second
				}
				f.clock.Set(now.Add(budget - time.Nanosecond))
				if _, err := f.engine.Advance(f.clock.Now()); err != nil {
					t.Fatal(err)
				}
				s, err := f.engine.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				if s.RetryQueueDepth != 0 || f.factory.sentCount() != int(attempt)+1 {
					t.Fatal("attempt fired before its original budget")
				}
				deadline := now.Add(budget)
				f.clock.Set(deadline)
				_, err = f.engine.Advance(deadline)
				if attempt == 3 {
					assertVerificationCode(t, err, FailureCodeTerminalSend)
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				retry, err := f.retry.Attempt(intent.Logical, attempt+1)
				if err != nil {
					t.Fatal(err)
				}
				if retry.BaseDelay != [3]time.Duration{100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}[attempt] {
					t.Fatal("retry delay changed")
				}
				now = deadline.Add(retry.Delay)
				f.clock.Set(now)
				if _, err := f.engine.Advance(now); err != nil {
					t.Fatal(err)
				}
				waitForEngineAdmissions(t, f.engine)
				packets := f.factory.sentPackets()
				if len(packets) != int(attempt)+2 || packets[len(packets)-1].ClientMsgNo != packets[0].ClientMsgNo || packets[len(packets)-1].ClientSeq == packets[len(packets)-2].ClientSeq {
					t.Fatal("retry identity/count changed")
				}
			}
			v := f.verifier.Snapshot()
			if v.RetryAttempts != 3 || v.Terminal != 1 || v.PendingCurrent != 0 || v.FirstAttemptFailures != 1 || f.factory.sentCount() != 4 {
				t.Fatal("retry exhaustion or cleanup changed")
			}
		})
	}
}

func TestFirstFiveACKBoundaryAndStop(t *testing.T) {
	for _, elapsed := range []time.Duration{2 * time.Second, 5*time.Second - time.Nanosecond, 5 * time.Second, 5*time.Second + time.Nanosecond} {
		t.Run(elapsed.String(), func(t *testing.T) {
			f := hotAttemptBudgetFactoryFixture(t, firstFiveConfig(t, pairedLocalP95Fixture(), 5*time.Second))
			uid, intent, started := hotAttemptBudgetStartSend(t, f, 182)
			ack := firstDeadlineAck(intent.Logical, f.factory.sentPackets()[0].ClientSeq)
			f.clock.Set(started.Add(elapsed))
			if err := f.verifier.HandleSendackAt(ack, f.clock.Now()); err != nil {
				t.Fatal(err)
			}
			if err := f.engine.ObserveSendack(uid, ack, nil); err != nil {
				t.Fatal(err)
			}
			late := uint64(0)
			if elapsed >= 5*time.Second {
				late = 1
			}
			assertFirstDeadlineCounters(t, f.verifier.Snapshot(), 1, 1, late, 0)
			if err := f.engine.Stop(); err != nil {
				t.Fatal(err)
			}
			if f.verifier.Snapshot().PendingCurrent != 0 {
				t.Fatal("Stop retained SEND")
			}
		})
	}
	t.Run("stop_before_deadline", func(t *testing.T) {
		f := hotAttemptBudgetFactoryFixture(t, firstFiveConfig(t, pairedLocalP95Fixture(), 5*time.Second))
		_, _, started := hotAttemptBudgetStartSend(t, f, 183)
		f.clock.Set(started.Add(3 * time.Second))
		if _, err := f.engine.Advance(f.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if err := f.engine.Stop(); err != nil {
			t.Fatal(err)
		}
		v := f.verifier.Snapshot()
		assertFirstDeadlineCounters(t, v, 1, 0, 0, 1)
		if v.RetryAttempts != 0 || v.TerminalReasons.SessionClosed != 1 || v.PendingCurrent != 0 {
			t.Fatal("Stop changed coverage or leaked ownership")
		}
	})
}

func TestFirstFiveConfigAndReportContract(t *testing.T) {
	cfg := firstFiveConfig(t, pairedLocalP95Fixture(), 5*time.Second)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(2100000000, 0)
	recorder, err := NewCheckpointRecorder(cfg, WorkerFence{RunID: cfg.RunID, AssignmentID: "first-five", Generation: 1}, start)
	if err != nil {
		t.Fatal(err)
	}
	r := recorder.buildReport(CheckpointQualification, start.Add(time.Minute), time.Minute, CoordinatorSnapshot{Generation: 1}, checkpointEvidenceFixture(false))
	if r.SchemaVersion != "wukongim/chat-lifecycle-report/v11" || r.ThresholdVersion != "wukongim/chat-lifecycle-thresholds/v5" {
		t.Fatal("native writer did not separate5s policy")
	}
	for _, invalid := range []Config{firstFiveConfig(t, FormalConfig(), 5*time.Second), firstFiveConfig(t, RehearsalConfig(), 5*time.Second), firstFiveConfig(t, LocalConfig(), 5*time.Second), firstFiveConfig(t, pairedLocalP95Fixture(), -time.Nanosecond), firstFiveConfig(t, pairedLocalP95Fixture(), 4*time.Second)} {
		if invalid.Validate() == nil {
			t.Fatal("nonlocal/nonpaired/non5s override accepted")
		}
	}
	body := firstFiveJSON(t, pairedP95PersistedReport(t), 5*time.Second)
	body = []byte(strings.ReplaceAll(strings.ReplaceAll(string(body), "chat-lifecycle-report/v10", "chat-lifecycle-report/v11"), "chat-lifecycle-thresholds/v4", "chat-lifecycle-thresholds/v5"))
	read := func(raw []byte) error {
		p := filepath.Join(t.TempDir(), "report.json")
		if err := os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
		_, err := ReadCurrentReport(p)
		return err
	}
	if err := read(body); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.ReplaceAll(string(body), "chat-lifecycle-report/v11", "chat-lifecycle-report/v10"),
		strings.ReplaceAll(string(body), "chat-lifecycle-thresholds/v5", "chat-lifecycle-thresholds/v4"),
		strings.ReplaceAll(string(body), `"first_attempt_timeout":5000000000`, `"first_attempt_timeout":0`),
		strings.ReplaceAll(string(body), `"first_attempt_timeout":5000000000`, `"first_attempt_timeout":null`),
		strings.ReplaceAll(string(body), `"first_attempt_timeout":5000000000`, `"FIRST_ATTEMPT_TIMEOUT":5000000000`),
		strings.ReplaceAll(string(body), `"first_attempt_timeout":5000000000`, `"first_attempt_timeout":5000000000,"first_attempt_timeout":5000000000`),
		strings.ReplaceAll(string(body), `"P95Limit":1000000000`, `"P95Limit":null`),
		strings.ReplaceAll(string(body), `"wukongim/chat-lifecycle-latency-gate/v3"`, `"wukongim/chat-lifecycle-latency-gate/v2"`),
	} {
		if bad == string(body) {
			t.Fatal("invalid-report fixture made no change")
		}
		if read([]byte(bad)) == nil {
			t.Fatal("invalid/relabeled new policy accepted")
		}
	}
	old := pairedP95PersistedReport(t)
	if err := read(old); err != nil {
		t.Fatal("old v10 rejected", err)
	}
	if read(firstFiveJSON(t, old, 5*time.Second)) == nil {
		t.Fatal("v10 silently acquired5s policy")
	}
}
