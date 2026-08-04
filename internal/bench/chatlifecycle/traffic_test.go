package chatlifecycle

import (
	"errors"
	"testing"
	"time"
)

func TestTrafficGeneratorPreservesFormalAggregateGrantAndMix(t *testing.T) {
	t.Parallel()
	cfg := FormalConfig()
	generator := newTrafficTestGenerator(t, cfg, time.Unix(1_700_000_000, 0))

	var total TrafficTickSnapshot
	directions := map[PersonDirection]uint64{}
	for tick := 0; tick < 10; tick++ {
		snapshot, err := generator.Tick([]uint64{10_000, 10_000, 10_000}, func(intent TrafficIntent) error {
			if intent.Canary {
				t.Fatal("primary tick emitted very-large canary")
			}
			if intent.Logical.ClientMsgNo == "" || len(intent.Packet.Payload) != intent.PayloadBytes {
				t.Fatalf("invalid primary intent: %+v", intent)
			}
			if intent.Kind == TrafficPerson {
				directions[intent.Direction]++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Tick(%d): %v", tick, err)
		}
		if snapshot.Released != 2_000 {
			t.Fatalf("tick %d released = %d, want 2000", tick, snapshot.Released)
		}
		total.Add(snapshot)
	}
	if total.Released != 20_000 || total.Person != 18_000 || total.Group != 2_000 {
		t.Fatalf("primary totals = %+v", total)
	}
	if got := total.PayloadCounts; got != [4]uint64{14_000, 5_000, 800, 200} {
		t.Fatalf("payload counts = %v", got)
	}
	if directions[DirectionAlternating] != 12_600 || directions[DirectionOneWay] != 5_400 {
		t.Fatalf("person directions = %v", directions)
	}
	if total.PayloadBytes != 14_000*256+5_000*1_024+800*4_096+200*16_384 {
		t.Fatalf("payload bytes = %d", total.PayloadBytes)
	}
	hotSet := generator.Snapshot().HotSet
	if hotSet.PersonChannels != 8_000 || hotSet.GroupChannels != 2_000 || hotSet.TotalChannels != 10_000 || hotSet.HistoricalGroupGrowth != 0 {
		t.Fatalf("hot set = %+v", hotSet)
	}
}

func TestTrafficGeneratorVeryLargeCanaryIsOncePerMinuteAndOutsidePrimaryRate(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)
	generator := newTrafficTestGenerator(t, FormalConfig(), start)

	if _, due, err := generator.NextCanary(start.Add(time.Minute - time.Nanosecond)); err != nil || due {
		t.Fatalf("early canary = due %v, err %v", due, err)
	}
	for minute := 1; minute <= 3; minute++ {
		intent, due, err := generator.NextCanary(start.Add(time.Duration(minute) * time.Minute))
		if err != nil {
			t.Fatalf("NextCanary(%d): %v", minute, err)
		}
		if !due || !intent.Canary || intent.GroupCategory != GroupVeryLarge {
			t.Fatalf("minute %d canary = %+v, due %v", minute, intent, due)
		}
		if generator.Snapshot().PrimaryReleased != 0 {
			t.Fatal("canary consumed primary SEND grant")
		}
		if _, duplicate, err := generator.NextCanary(start.Add(time.Duration(minute) * time.Minute)); err != nil || duplicate {
			t.Fatalf("duplicate minute %d canary = due %v, err %v", minute, duplicate, err)
		}
	}
	if got := generator.Snapshot().Canaries; got != 3 {
		t.Fatalf("canaries = %d, want 3", got)
	}
}

func TestRetrySchedulerUsesOnlyApprovedStableIdentityRetries(t *testing.T) {
	t.Parallel()
	cfg := LocalConfig()
	identity, err := NewIdentitySpace("retry-runtime-test", 79, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	policy, err := NewRetryPolicy(identity, cfg.Workload.Retry)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	logical, err := model.NewLogicalSend(0, 44, TrafficPerson, identity.UID(1), identity.UID(2))
	if err != nil {
		t.Fatalf("NewLogicalSend: %v", err)
	}
	payload, err := model.BuildPayload(logical, 256)
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	intent := TrafficIntent{Logical: logical, Packet: packetForTrafficIntent(logical, payload), Kind: TrafficPerson, PayloadBytes: len(payload)}
	scheduler, err := NewRetryScheduler(policy, 4)
	if err != nil {
		t.Fatalf("NewRetryScheduler: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	for completedAttempt := uint8(0); completedAttempt < 3; completedAttempt++ {
		retry, err := scheduler.Schedule(intent, completedAttempt, now)
		if err != nil {
			t.Fatalf("Schedule after attempt %d: %v", completedAttempt, err)
		}
		want, err := policy.Attempt(logical, completedAttempt+1)
		if err != nil {
			t.Fatalf("policy Attempt: %v", err)
		}
		if retry.Attempt.ClientMsgNo != logical.ClientMsgNo || retry.Due != now.Add(want.Delay) {
			t.Fatalf("retry = %+v, want due %v stable %q", retry, now.Add(want.Delay), logical.ClientMsgNo)
		}
		if got := scheduler.PopDue(retry.Due.Add(-time.Nanosecond), 1); len(got) != 0 {
			t.Fatalf("early PopDue = %v", got)
		}
		got := scheduler.PopDue(retry.Due, 1)
		if len(got) != 1 || got[0].Attempt.ClientMsgNo != logical.ClientMsgNo || got[0].Attempt.Attempt != completedAttempt+1 {
			t.Fatalf("due retry = %+v", got)
		}
		now = retry.Due
	}
	if _, err := scheduler.Schedule(intent, 3, now); !errors.Is(err, ErrRetryLimitReached) {
		t.Fatalf("fourth retry error = %v, want ErrRetryLimitReached", err)
	}
}

func TestRetrySchedulerCapacityIsHarnessInvalidAndObservable(t *testing.T) {
	t.Parallel()
	cfg := LocalConfig()
	identity, err := NewIdentitySpace("retry-capacity-test", 83, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	policy, err := NewRetryPolicy(identity, cfg.Workload.Retry)
	if err != nil {
		t.Fatalf("NewRetryPolicy: %v", err)
	}
	scheduler, err := NewRetryScheduler(policy, 1)
	if err != nil {
		t.Fatalf("NewRetryScheduler: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	for ordinal := uint64(1); ordinal <= 2; ordinal++ {
		logical, err := model.NewLogicalSend(0, ordinal, TrafficGroup, identity.UID(1), "group-capacity")
		if err != nil {
			t.Fatalf("NewLogicalSend: %v", err)
		}
		intent := TrafficIntent{Logical: logical, Kind: TrafficGroup}
		_, err = scheduler.Schedule(intent, 0, now)
		if ordinal == 1 && err != nil {
			t.Fatalf("first Schedule: %v", err)
		}
		if ordinal == 2 {
			var runtimeErr *RuntimeError
			if !errors.As(err, &runtimeErr) || runtimeErr.Classification() != SyncClassificationHarnessInvalid || runtimeErr.Code() != RuntimeFailureRetryQueueSaturated {
				t.Fatalf("capacity error = %#v", err)
			}
		}
	}
	snapshot := scheduler.Snapshot()
	if snapshot.Depth != 1 || snapshot.Peak != 1 || snapshot.Capacity != 1 || snapshot.Saturation != 1 {
		t.Fatalf("retry snapshot = %+v", snapshot)
	}
}

func newTrafficTestGenerator(t *testing.T, cfg Config, start time.Time) *TrafficGenerator {
	t.Helper()
	identity, err := NewIdentitySpace("traffic-test", 73, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	graph, err := NewRelationshipGraph(identity)
	if err != nil {
		t.Fatalf("NewRelationshipGraph: %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}
	generator, err := NewTrafficGenerator(TrafficGeneratorConfig{
		Identity: identity, Model: model, Graph: graph, Catalog: catalog,
		Workload: cfg.Workload, Start: start,
	})
	if err != nil {
		t.Fatalf("NewTrafficGenerator: %v", err)
	}
	return generator
}
