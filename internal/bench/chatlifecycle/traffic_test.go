package chatlifecycle

import (
	"errors"
	"testing"
	"time"
)

func TestTrafficGeneratorPreservesFormalAggregateGrantAndMix(t *testing.T) {
	t.Parallel()
	cfg := FormalConfig()
	generators := make([]*TrafficGenerator, cfg.Workload.Workers)
	for workerID := range generators {
		generators[workerID] = newTrafficTestGenerator(t, cfg, time.Unix(1_700_000_000, 0), uint64(workerID))
	}

	var total TrafficTickSnapshot
	directions := map[PersonDirection]uint64{}
	for tick := 0; tick < 10; tick++ {
		var snapshot TrafficTickSnapshot
		for workerID, generator := range generators {
			workerSnapshot, err := generator.Tick([]uint64{10_000, 10_000, 10_000}, func(intent TrafficIntent) error {
				if intent.Canary {
					t.Fatal("primary tick emitted very-large canary")
				}
				if intent.Packet != nil || intent.Logical.ClientMsgNo != "" || intent.Logical.Sender != "" || intent.Logical.Target != "" {
					t.Fatalf("generator grant claimed a concrete online route: %+v", intent)
				}
				if intent.Logical.LogicalSend == 0 || intent.Logical.WorkerID != uint32(workerID) {
					t.Fatalf("invalid route-free primary grant: %+v", intent)
				}
				if intent.Kind == TrafficPerson {
					directions[intent.Direction]++
				}
				return nil
			})
			if err != nil {
				t.Fatalf("Tick(%d, worker=%d): %v", tick, workerID, err)
			}
			snapshot.Add(workerSnapshot)
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
	hotSet := generators[0].Snapshot().HotSet
	if hotSet.PersonChannels != 2_667 || hotSet.GroupChannels != 2_000 || hotSet.TotalChannels != 4_667 || hotSet.HistoricalGroupGrowth != 0 {
		t.Fatalf("hot set = %+v", hotSet)
	}
}

func TestTrafficGeneratorsPartitionFormalPersonHotSetByWorker(t *testing.T) {
	cfg := FormalConfig()
	start := time.Unix(1_700_000_000, 0)
	want := []int{2_667, 2_667, 2_666}
	total := 0
	for workerID, wantPersonChannels := range want {
		generator := newTrafficTestGenerator(t, cfg, start, uint64(workerID))
		hotSet := generator.Snapshot().HotSet
		if hotSet.PersonChannels != wantPersonChannels || hotSet.GroupChannels != 2_000 || hotSet.TotalChannels != wantPersonChannels+2_000 {
			t.Fatalf("worker %d hot set = %+v, want person=%d group=2000", workerID, hotSet, wantPersonChannels)
		}
		total += hotSet.PersonChannels
	}
	if total != cfg.Workload.HotSet.PersonChannels {
		t.Fatalf("partitioned person hot set = %d, want %d", total, cfg.Workload.HotSet.PersonChannels)
	}
}

func TestTrafficGeneratorSingleWorkerRetainsGlobalPersonHotSet(t *testing.T) {
	cfg := FormalConfig()
	cfg.Workload.Workers = 1
	generator := newTrafficTestGenerator(t, cfg, time.Unix(1_700_000_000, 0), 0)
	hotSet := generator.Snapshot().HotSet
	if hotSet.PersonChannels != 8_000 || hotSet.GroupChannels != 2_000 || hotSet.TotalChannels != 10_000 {
		t.Fatalf("single-worker hot set = %+v", hotSet)
	}
}

func TestTrafficGeneratorsPartitionFormalGlobalGrantByWorker(t *testing.T) {
	cfg := FormalConfig()
	start := time.Unix(1_700_000_000, 0)
	identity, err := NewIdentitySpace("traffic-worker-partition", 97, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}

	type grantIdentity struct {
		worker  uint32
		logical uint64
	}
	seen := make(map[grantIdentity]struct{}, cfg.Workload.SendRatePerSecond)
	var total TrafficTickSnapshot
	for workerID := 0; workerID < cfg.Workload.Workers; workerID++ {
		generator, err := NewTrafficGenerator(TrafficGeneratorConfig{
			Identity: identity, Model: model, Catalog: catalog, Workload: cfg.Workload, Start: start,
			WorkerID: uint64(workerID), WorkerCount: uint64(cfg.Workload.Workers),
		})
		if err != nil {
			t.Fatalf("NewTrafficGenerator(worker=%d): %v", workerID, err)
		}
		tick, err := generator.Tick([]uint64{10_000, 10_000, 10_000}, func(intent TrafficIntent) error {
			if intent.Logical.WorkerID != uint32(workerID) {
				t.Fatalf("worker %d emitted worker %d", workerID, intent.Logical.WorkerID)
			}
			key := grantIdentity{worker: intent.Logical.WorkerID, logical: intent.Logical.LogicalSend}
			if _, duplicate := seen[key]; duplicate {
				t.Fatalf("duplicate global grant %+v", key)
			}
			seen[key] = struct{}{}
			return nil
		})
		if err != nil {
			t.Fatalf("Tick(worker=%d): %v", workerID, err)
		}
		total.Add(tick)
	}
	if total.Released != 2_000 || total.Person != 1_800 || total.Group != 200 || total.PayloadCounts != [4]uint64{1_400, 500, 80, 20} {
		t.Fatalf("partitioned formal tick = %+v", total)
	}
	if len(seen) != cfg.Workload.SendRatePerSecond {
		t.Fatalf("unique grants = %d, want %d", len(seen), cfg.Workload.SendRatePerSecond)
	}
}

func TestTrafficGeneratorsApplyOneCoordinatorGrantWithoutLocalAllocators(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "traffic-external-grant"
	assignments, err := BuildCoordinatorAssignments(cfg, 8)
	if err != nil {
		t.Fatalf("BuildCoordinatorAssignments() error = %v", err)
	}
	plan, err := NewCoordinatorGrantPlan(assignments)
	if err != nil {
		t.Fatalf("NewCoordinatorGrantPlan() error = %v", err)
	}
	grant, err := plan.Tick([coordinatorWorkerCount]uint64{^uint64(0), ^uint64(0), ^uint64(0)})
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}

	var total uint64
	for workerID := uint64(0); workerID < uint64(cfg.Workload.Workers); workerID++ {
		generator := newTrafficTestGenerator(t, cfg, time.Unix(1_700_000_000, 0), workerID)
		emitted := uint64(0)
		snapshot, err := generator.ApplyGrant(grant.Released[workerID], func(TrafficIntent) error {
			emitted++
			return nil
		})
		if err != nil {
			t.Fatalf("worker %d ApplyGrant() error = %v", workerID, err)
		}
		if emitted != grant.Released[workerID] || snapshot.Released != grant.Released[workerID] {
			t.Fatalf("worker %d emitted/snapshot = %d/%d, want %d", workerID, emitted, snapshot.Released, grant.Released[workerID])
		}
		total += emitted
	}
	if total != uint64(cfg.Workload.SendRatePerSecond) {
		t.Fatalf("external grant total = %d, want exact global rate %d", total, cfg.Workload.SendRatePerSecond)
	}
}

func TestTrafficGeneratorRetainsAcceptedPrefixWhenExternalGrantFails(t *testing.T) {
	cfg := LocalConfig()
	cfg.RunID = "traffic-external-grant-failure"
	generator := newTrafficTestGenerator(t, cfg, time.Unix(1_700_000_000, 0), 0)
	emitted := 0
	wantErr := errors.New("injected emitter failure")
	snapshot, err := generator.ApplyGrant(3, func(TrafficIntent) error {
		emitted++
		if emitted == 2 {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ApplyGrant() error = %v, want injected failure", err)
	}
	if snapshot.Released != 1 {
		t.Fatalf("returned accepted prefix = %d, want 1", snapshot.Released)
	}
	if got := generator.Snapshot().PrimaryReleased; got != 1 {
		t.Fatalf("retained accepted prefix = %d, want 1 so failed grant is never replayed", got)
	}
}

func TestTrafficGeneratorsEmitEveryFormalGroupGrantOnlyOnCatalogOwner(t *testing.T) {
	cfg := FormalConfig()
	start := time.Unix(1_700_000_000, 0)
	generators := make([]*TrafficGenerator, cfg.Workload.Workers)
	verifiers := make([]*Verifier, cfg.Workload.Workers)
	for workerID := range generators {
		generators[workerID] = newTrafficTestGenerator(t, cfg, start, uint64(workerID))
		evidence, err := NewEvidenceRecorder(1, 1)
		if err != nil {
			t.Fatalf("worker %d NewEvidenceRecorder: %v", workerID, err)
		}
		verifiers[workerID], err = NewVerifier(generators[workerID].model, VerifierConfig{
			PendingCapacity: 512, SequenceCapacity: 512, CorrelationCapacity: 512, CorrelationDeadline: time.Minute,
		}, evidence)
		if err != nil {
			t.Fatalf("worker %d NewVerifier: %v", workerID, err)
		}
	}

	var total TrafficTickSnapshot
	categories := map[GroupCategory]int{}
	sampledGroups := 0
	for workerID, generator := range generators {
		tick, err := generator.Tick([]uint64{10_000, 10_000, 10_000}, func(intent TrafficIntent) error {
			if intent.Kind != TrafficGroup {
				return nil
			}
			groupIndex, ok := generator.catalog.IndexFromGroupID(intent.ChannelID)
			if !ok {
				t.Fatalf("worker %d emitted unknown group %q", workerID, intent.ChannelID)
			}
			owner := groupIndex % uint64(cfg.Workload.Workers)
			if owner != uint64(workerID) {
				t.Fatalf("worker %d emitted group %d owned by worker %d", workerID, groupIndex, owner)
			}
			categories[intent.GroupCategory]++
			sampled, sampleErr := verifiers[workerID].ShouldCorrelate(intent.Logical)
			if sampleErr != nil {
				t.Fatalf("worker %d ShouldCorrelate: %v", workerID, sampleErr)
			}
			if sampled {
				sampledGroups++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("worker %d Tick: %v", workerID, err)
		}
		total.Add(tick)
	}
	if total.Released != 2_000 || total.Person != 1_800 || total.Group != 200 || total.PayloadCounts != [4]uint64{1_400, 500, 80, 20} {
		t.Fatalf("owned formal aggregate = %+v", total)
	}
	if categories[GroupSmall] != 160 || categories[GroupMedium] != 30 || categories[GroupLarge] != 10 || sampledGroups != 2 {
		t.Fatalf("owned formal group normal/sample categories = %v sampled=%d, want 160/30/10 and 198/2", categories, sampledGroups)
	}

	dueCount := 0
	for workerID, generator := range generators {
		intent, due, err := generator.NextCanary(start.Add(time.Minute))
		if err != nil {
			t.Fatalf("worker %d NextCanary: %v", workerID, err)
		}
		if !due {
			continue
		}
		dueCount++
		groupIndex, ok := generator.catalog.IndexFromGroupID(intent.ChannelID)
		if !ok || groupIndex%uint64(cfg.Workload.Workers) != uint64(workerID) {
			t.Fatalf("worker %d emitted non-owned canary group %d/%v: %+v", workerID, groupIndex, ok, intent)
		}
		if err := generator.commitCanary(); err != nil {
			t.Fatalf("worker %d commitCanary: %v", workerID, err)
		}
	}
	if dueCount != 1 {
		t.Fatalf("owned canary count = %d, want 1", dueCount)
	}
}

func TestTrafficGeneratorVeryLargeCanaryIsOncePerMinuteAndOutsidePrimaryRate(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_700_000_000, 0)
	cfg := FormalConfig()
	generators := make([]*TrafficGenerator, cfg.Workload.Workers)
	for workerID := range generators {
		generators[workerID] = newTrafficTestGenerator(t, cfg, start, uint64(workerID))
	}

	for workerID, generator := range generators {
		if _, due, err := generator.NextCanary(start.Add(time.Minute - time.Nanosecond)); err != nil || due {
			t.Fatalf("worker %d early canary = due %v, err %v", workerID, due, err)
		}
	}
	for minute := 1; minute <= 3; minute++ {
		dueCount := 0
		for workerID, generator := range generators {
			intent, due, err := generator.NextCanary(start.Add(time.Duration(minute) * time.Minute))
			if err != nil {
				t.Fatalf("NextCanary(%d, worker=%d): %v", minute, workerID, err)
			}
			if due {
				dueCount++
				if !intent.Canary || intent.GroupCategory != GroupVeryLarge || intent.Logical.WorkerID != uint32(workerID) {
					t.Fatalf("minute %d worker %d canary = %+v", minute, workerID, intent)
				}
				if err := generator.commitCanary(); err != nil {
					t.Fatalf("minute %d worker %d commitCanary: %v", minute, workerID, err)
				}
			}
			if generator.Snapshot().PrimaryReleased != 0 {
				t.Fatal("canary consumed primary SEND grant")
			}
			if _, duplicate, err := generator.NextCanary(start.Add(time.Duration(minute) * time.Minute)); err != nil || duplicate {
				t.Fatalf("duplicate minute %d worker %d = due %v, err %v", minute, workerID, duplicate, err)
			}
		}
		if dueCount != 1 {
			t.Fatalf("minute %d canaries = %d, want 1", minute, dueCount)
		}
	}
	var canaries uint64
	for _, generator := range generators {
		canaries += generator.Snapshot().Canaries
	}
	if got := canaries; got != 3 {
		t.Fatalf("canaries = %d, want 3", got)
	}
}

func TestTrafficGeneratorRejectsPrimaryCategoryWithoutOneGroupPerWorker(t *testing.T) {
	cfg := LocalConfig()
	cfg.Workload.Workers = 4
	identity, err := NewIdentitySpace("traffic-owner-capacity", 73, 4)
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}
	_, err = NewTrafficGenerator(TrafficGeneratorConfig{
		Identity: identity, Model: model, Catalog: catalog, Workload: cfg.Workload,
		Start: time.Unix(1_700_000_000, 0), WorkerID: 0, WorkerCount: 4,
	})
	if !errors.Is(err, errTrafficGeneratorConfig) {
		t.Fatalf("NewTrafficGenerator with 3 medium groups/4 workers error = %v, want %v", err, errTrafficGeneratorConfig)
	}
}

func TestLogicalDomainEncodingHasCheckedSeventyTwoHourBudget(t *testing.T) {
	t.Parallel()
	const seventyTwoHourPrimary = uint64(72 * 60 * 60 * 2_000)
	seen := map[uint64]struct{}{}
	for domain := LogicalDomainPrimary; domain <= LogicalDomainCanary; domain++ {
		ordinal, err := scopedLogicalOrdinal(7, domain, seventyTwoHourPrimary)
		if err != nil {
			t.Fatalf("scopedLogicalOrdinal(%d): %v", domain, err)
		}
		if _, duplicate := seen[ordinal]; duplicate {
			t.Fatalf("domain %d reused ordinal %d", domain, ordinal)
		}
		seen[ordinal] = struct{}{}
	}
	if _, err := scopedLogicalOrdinal(maxLogicalGeneration+1, LogicalDomainPrimary, 0); err == nil {
		t.Fatal("generation overflow accepted")
	}
	if _, err := scopedLogicalOrdinal(1, LogicalDomainPrimary, maxLogicalOrdinal+1); err == nil {
		t.Fatal("domain ordinal overflow accepted")
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

func newTrafficTestGenerator(t *testing.T, cfg Config, start time.Time, workerID uint64) *TrafficGenerator {
	t.Helper()
	identity, err := NewIdentitySpace("traffic-test", 73, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	model, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel: %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog: %v", err)
	}
	generator, err := NewTrafficGenerator(TrafficGeneratorConfig{
		Identity: identity, Model: model, Catalog: catalog,
		Workload: cfg.Workload, Start: start, WorkerID: workerID, WorkerCount: uint64(cfg.Workload.Workers),
	})
	if err != nil {
		t.Fatalf("NewTrafficGenerator: %v", err)
	}
	return generator
}
