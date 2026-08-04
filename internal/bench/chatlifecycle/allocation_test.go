package chatlifecycle

import (
	"fmt"
	"runtime"
	"sort"
	"testing"
)

const (
	formalVirtualDayUsers = uint64(250_000)
	retainedSampleCount   = 3
	retainedHeapNoise     = uint64(256 << 10)
	retainedObjectNoise   = uint64(2_048)
)

var allocationSink struct {
	uid          string
	index        uint64
	edges        ForwardRelationshipSet
	traffic      TrafficKind
	payloadBytes int
	login        LoginSchedule
	channel      ChannelSchedule
	group        Group
	groupMember  string
}

func TestLifecycleModelAllocationBudgets(t *testing.T) {
	fixture := newAllocationFixture(t)

	tests := []struct {
		name   string
		budget float64
		run    func()
	}{
		{
			name: "identity UID round trip",
			// The measured baseline is two allocations for the formatted UID.
			// One allocation of headroom avoids pinning compiler coalescing details.
			budget: 3,
			run: func() {
				uid := fixture.identity.UID(123_456)
				index, ok := fixture.identity.IndexFromUID(uid)
				if !ok {
					panic("lifecycle UID did not round trip")
				}
				allocationSink.uid, allocationSink.index = uid, index
			},
		},
		{
			name: "five-edge reconstruction",
			// Fifteen result strings are necessary for five complete edges. The
			// original 25-allocation baseline repeated the owner UID for every
			// edge; 23 permits eight transient formatting/canonicalization
			// allocations without allowing that repeated-work regression back.
			budget: 23,
			run: func() {
				edges, err := fixture.graph.Outgoing(fixture.fiveEdgeOwner)
				if err != nil {
					panic(err)
				}
				allocationSink.edges = edges
			},
		},
		{
			name: "traffic and payload choice",
			// Exact-cycle choices are integer-only and need no heap allocation.
			budget: 0,
			run: func() {
				traffic, err := fixture.traffic.TrafficFor(42)
				if err != nil {
					panic(err)
				}
				payloadBytes, err := fixture.traffic.PayloadSizeFor(42)
				if err != nil {
					panic(err)
				}
				allocationSink.traffic, allocationSink.payloadBytes = traffic, payloadBytes
			},
		},
		{
			name: "login schedule",
			// One semantic hash allocation is currently necessary; one spare
			// allocation leaves room for compiler/runtime variation.
			budget: 2,
			run: func() {
				login, err := fixture.schedule.Login(42)
				if err != nil {
					panic(err)
				}
				allocationSink.login = login
			},
		},
		{
			name: "channel schedule",
			// The measured maximum lifecycle path uses four allocations for
			// three semantic hashes plus call scaffolding. One allocation of
			// headroom avoids pinning that exact implementation detail.
			budget: 5,
			run: func() {
				channel, err := fixture.schedule.Channel(fixture.longChannelOrdinal, 42, 43)
				if err != nil {
					panic(err)
				}
				allocationSink.channel = channel
			},
		},
		{
			name: "group and one member reconstruction",
			// Group ID and member UID formatting account for the five-allocation
			// baseline. One spare allocation still forbids member-slice creation.
			budget: 6,
			run: func() {
				group, err := fixture.groups.Group(1_999)
				if err != nil {
					panic(err)
				}
				member, err := group.MemberUID(group.MemberCount - 1)
				if err != nil {
					panic(err)
				}
				allocationSink.group, allocationSink.groupMember = group, member
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := testing.AllocsPerRun(1_000, tt.run)
			if got > tt.budget {
				t.Fatalf("allocations/run = %.1f, budget %.1f", got, tt.budget)
			}
		})
	}
}

func TestLifecycleVirtualDayDoesNotRetainHistory(t *testing.T) {
	const smallHistoryUsers = uint64(5_000)
	small := retainedHistorySamples(t, smallHistoryUsers)
	large := retainedHistorySamples(t, formalVirtualDayUsers)

	for sampleIndex, sample := range large {
		if sample.summary.users != formalVirtualDayUsers {
			t.Fatalf("large sample %d users = %d, want %d", sampleIndex, sample.summary.users, formalVirtualDayUsers)
		}
		if sample.summary.relationships != 1_000_000 {
			t.Fatalf("large sample %d relationships = %d, want 1000000", sampleIndex, sample.summary.relationships)
		}
		if sample.summary.minimumMatureDegree < 6 || sample.summary.maximumMatureDegree > MaxUserRelationships {
			t.Fatalf("large sample %d mature degree range = %d..%d, want 6..%d", sampleIndex, sample.summary.minimumMatureDegree, sample.summary.maximumMatureDegree, MaxUserRelationships)
		}
		if sample.summary.checksum == 0 {
			t.Fatalf("large sample %d checksum is zero", sampleIndex)
		}
	}

	// Compare medians after a warm-up and two forced GCs per sample. Relative
	// retained growth is the signal: a history slice/map would scale by 50x,
	// while transient UID/channel strings disappear. The fixed allowances cover
	// runtime and test-runner noise, not historical model state.
	smallHeap, smallObjects := medianRetained(small)
	largeHeap, largeObjects := medianRetained(large)
	if largeHeap > smallHeap+retainedHeapNoise {
		t.Fatalf("retained heap after %d users = %d bytes, after %d = %d; allowed relative noise = %d", formalVirtualDayUsers, largeHeap, smallHistoryUsers, smallHeap, retainedHeapNoise)
	}
	if largeObjects > smallObjects+retainedObjectNoise {
		t.Fatalf("retained objects after %d users = %d, after %d = %d; allowed relative noise = %d", formalVirtualDayUsers, largeObjects, smallHistoryUsers, smallObjects, retainedObjectNoise)
	}
}

type allocationFixture struct {
	identity           *IdentitySpace
	graph              RelationshipGraph
	traffic            TrafficModel
	schedule           ScheduleModel
	groups             GroupCatalog
	fiveEdgeOwner      uint64
	longChannelOrdinal uint64
}

func newAllocationFixture(t *testing.T) allocationFixture {
	t.Helper()
	cfg := FormalConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	graph, err := NewRelationshipGraph(identity)
	if err != nil {
		t.Fatalf("NewRelationshipGraph() error = %v", err)
	}
	traffic, err := NewTrafficModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewTrafficModel() error = %v", err)
	}
	schedule, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel() error = %v", err)
	}
	groups, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog() error = %v", err)
	}

	fiveEdgeOwner := firstOrdinalWithDegree(graph, MaxForwardRelationships)
	longChannelOrdinal := firstLifecycleOrdinal(t, schedule, LifecycleLong)
	return allocationFixture{
		identity:           identity,
		graph:              graph,
		traffic:            traffic,
		schedule:           schedule,
		groups:             groups,
		fiveEdgeOwner:      fiveEdgeOwner,
		longChannelOrdinal: longChannelOrdinal,
	}
}

func firstOrdinalWithDegree(graph RelationshipGraph, want int) uint64 {
	for ordinal := uint64(0); ordinal < uint64(len(relationshipDegreePattern)); ordinal++ {
		if int(graph.Degree(ordinal)) == want {
			return ordinal
		}
	}
	panic(fmt.Sprintf("relationship degree %d is absent", want))
}

func firstLifecycleOrdinal(t *testing.T, schedule ScheduleModel, want LifecycleClass) uint64 {
	t.Helper()
	for ordinal := uint64(0); ordinal < distributionCycle; ordinal++ {
		channel, err := schedule.Channel(ordinal, 42, 43)
		if err != nil {
			t.Fatalf("Channel(%d) error = %v", ordinal, err)
		}
		if channel.Class == want {
			return ordinal
		}
	}
	t.Fatalf("lifecycle class %d is absent", want)
	return 0
}

type historySummary struct {
	users               uint64
	relationships       uint64
	minimumMatureDegree int
	maximumMatureDegree int
	checksum            uint64
}

func scanLifecycleHistory(fixture allocationFixture, users uint64) (historySummary, error) {
	summary := historySummary{users: users, minimumMatureDegree: MaxUserRelationships}
	for owner := uint64(0); owner < users; owner++ {
		uid := fixture.identity.UID(owner)
		uidIndex, ok := fixture.identity.IndexFromUID(uid)
		if !ok || uidIndex != owner {
			return historySummary{}, fmt.Errorf("UID for owner %d did not round trip", owner)
		}

		outgoing, err := fixture.graph.Outgoing(owner)
		if err != nil {
			return historySummary{}, fmt.Errorf("Outgoing(%d): %w", owner, err)
		}
		summary.relationships += uint64(outgoing.Count)
		for edgeIndex := 0; edgeIndex < outgoing.Count; edgeIndex++ {
			edge := outgoing.Items[edgeIndex]
			if edge.OwnerIndex != owner || edge.PeerIndex <= owner {
				return historySummary{}, fmt.Errorf("edge %d for owner %d is not forward", edgeIndex, owner)
			}
			summary.checksum = mixHistoryChecksum(summary.checksum, edge.OwnerIndex, edge.PeerIndex, uint64(len(edge.PersonChannelID)))
		}

		if owner >= MaxForwardRelationships {
			matureDegree := fixture.graph.Incoming(owner).Count + outgoing.Count
			if matureDegree < summary.minimumMatureDegree {
				summary.minimumMatureDegree = matureDegree
			}
			if matureDegree > summary.maximumMatureDegree {
				summary.maximumMatureDegree = matureDegree
			}
		}

		traffic, err := fixture.traffic.TrafficFor(owner)
		if err != nil {
			return historySummary{}, fmt.Errorf("TrafficFor(%d): %w", owner, err)
		}
		payloadBytes, err := fixture.traffic.PayloadSizeFor(owner)
		if err != nil {
			return historySummary{}, fmt.Errorf("PayloadSizeFor(%d): %w", owner, err)
		}
		login, err := fixture.schedule.Login(owner)
		if err != nil {
			return historySummary{}, fmt.Errorf("Login(%d): %w", owner, err)
		}
		channel, err := fixture.schedule.Channel(summary.relationships, owner, owner+1)
		if err != nil {
			return historySummary{}, fmt.Errorf("Channel(%d): %w", owner, err)
		}
		group, err := fixture.groups.Group(owner % uint64(fixture.groups.Count()))
		if err != nil {
			return historySummary{}, fmt.Errorf("Group(%d): %w", owner, err)
		}
		// Reconstruct exactly one member, including the 100,000-member canary;
		// never materialize a member slice.
		memberUID, err := group.MemberUID(group.MemberCount - 1)
		if err != nil {
			return historySummary{}, fmt.Errorf("Group(%d).MemberUID(): %w", owner, err)
		}
		summary.checksum = mixHistoryChecksum(
			summary.checksum,
			uint64(traffic),
			uint64(payloadBytes),
			uint64(login.Identity),
			uint64(channel.Class),
			uint64(len(group.ID)+len(memberUID)),
		)
	}
	return summary, nil
}

func mixHistoryChecksum(checksum uint64, values ...uint64) uint64 {
	for _, value := range values {
		checksum ^= value + 0x9e3779b97f4a7c15 + checksum<<6 + checksum>>2
	}
	return checksum
}

type retainedHistorySample struct {
	heapBytes   uint64
	heapObjects uint64
	summary     historySummary
}

func retainedHistorySamples(t *testing.T, users uint64) []retainedHistorySample {
	t.Helper()
	samples := make([]retainedHistorySample, 0, retainedSampleCount)
	for sampleIndex := 0; sampleIndex < retainedSampleCount; sampleIndex++ {
		fixture := newAllocationFixture(t)
		if _, err := scanLifecycleHistory(fixture, 64); err != nil {
			t.Fatalf("warm-up scan: %v", err)
		}
		runtime.GC()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		summary, err := scanLifecycleHistory(fixture, users)
		if err != nil {
			t.Fatalf("history scan %d: %v", sampleIndex, err)
		}
		runtime.GC()
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(fixture)
		runtime.KeepAlive(summary)

		samples = append(samples, retainedHistorySample{
			heapBytes:   positiveDelta(after.HeapAlloc, before.HeapAlloc),
			heapObjects: positiveDelta(after.HeapObjects, before.HeapObjects),
			summary:     summary,
		})
	}
	return samples
}

func positiveDelta(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func medianRetained(samples []retainedHistorySample) (heapBytes, heapObjects uint64) {
	heapValues := make([]uint64, len(samples))
	objectValues := make([]uint64, len(samples))
	for index, sample := range samples {
		heapValues[index] = sample.heapBytes
		objectValues[index] = sample.heapObjects
	}
	sort.Slice(heapValues, func(i, j int) bool { return heapValues[i] < heapValues[j] })
	sort.Slice(objectValues, func(i, j int) bool { return objectValues[i] < objectValues[j] })
	return heapValues[len(heapValues)/2], objectValues[len(objectValues)/2]
}
