package chatlifecycle

import (
	"errors"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func newTestRelationshipGraph(t *testing.T) (RelationshipGraph, *IdentitySpace) {
	return newTestRelationshipGraphWithWorkers(t, 1)
}

func newTestRelationshipGraphWithWorkers(t *testing.T, workers uint64) (RelationshipGraph, *IdentitySpace) {
	t.Helper()
	space, err := NewIdentitySpace("relationship-test", 71, workers)
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	graph, err := NewRelationshipGraph(space)
	if err != nil {
		t.Fatalf("NewRelationshipGraph() error = %v", err)
	}
	return graph, space
}

func TestNewRelationshipGraphRejectsNilIdentity(t *testing.T) {
	_, err := NewRelationshipGraph(nil)
	if !errors.Is(err, errRelationshipIdentityRequired) {
		t.Fatalf("NewRelationshipGraph(nil) error = %v, want %v", err, errRelationshipIdentityRequired)
	}
}

func TestRelationshipGraphVirtualDayHasExactDegreeDistribution(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	const owners = uint64(250_000)
	var counts [6]uint64
	var relationships uint64

	for owner := uint64(0); owner < owners; owner++ {
		outgoing, err := graph.Outgoing(owner)
		if err != nil {
			t.Fatalf("Outgoing(%d) error = %v", owner, err)
		}
		degree := outgoing.Count
		counts[degree]++
		relationships += uint64(degree)
		for offset := 0; offset < degree; offset++ {
			edge := outgoing.Items[offset]
			wantPeer := owner + uint64(offset) + 1
			if edge.OwnerIndex != owner || edge.PeerIndex != wantPeer {
				t.Fatalf("Outgoing(%d)[%d] endpoints = %d -> %d, want %d -> %d", owner, offset, edge.OwnerIndex, edge.PeerIndex, owner, wantPeer)
			}
			if edge.AvailableAtIndex != edge.PeerIndex {
				t.Fatalf("edge %d -> %d available at %d", edge.OwnerIndex, edge.PeerIndex, edge.AvailableAtIndex)
			}
			if offset > 0 && outgoing.Items[offset-1].PeerIndex >= edge.PeerIndex {
				t.Fatalf("Outgoing(%d) is not forward-unique", owner)
			}
		}
	}

	if counts[3] != 62_500 || counts[4] != 125_000 || counts[5] != 62_500 {
		t.Fatalf("degree counts 3/4/5 = %d/%d/%d, want 62500/125000/62500", counts[3], counts[4], counts[5])
	}
	if relationships != 1_000_000 {
		t.Fatalf("relationships = %d, want 1000000", relationships)
	}
}

func TestRelationshipGraphPartitionsEveryEdgeWithinOwnerWorker(t *testing.T) {
	graph, identity := newTestRelationshipGraphWithWorkers(t, 3)
	const ownersPerWorker = uint64(10_000)
	var relationships uint64
	for workerID := uint64(0); workerID < identity.Workers(); workerID++ {
		for localOwner := uint64(0); localOwner < ownersPerWorker; localOwner++ {
			ownerIndex, err := identity.GlobalIndex(workerID, localOwner)
			if err != nil {
				t.Fatalf("GlobalIndex(%d, %d): %v", workerID, localOwner, err)
			}
			outgoing, err := graph.Outgoing(ownerIndex)
			if err != nil {
				t.Fatalf("Outgoing(%d): %v", ownerIndex, err)
			}
			relationships += uint64(outgoing.Count)
			for edgeIndex := 0; edgeIndex < outgoing.Count; edgeIndex++ {
				edge := outgoing.Items[edgeIndex]
				ownerWorker, _ := identity.Owner(edge.OwnerIndex)
				peerWorker, _ := identity.Owner(edge.PeerIndex)
				if ownerWorker != workerID || peerWorker != workerID {
					t.Fatalf("worker %d local owner %d edge %d crosses workers: %+v owners=%d/%d", workerID, localOwner, edgeIndex, edge, ownerWorker, peerWorker)
				}
			}
		}
	}
	if want := ownersPerWorker * identity.Workers() * 4; relationships != want {
		t.Fatalf("relationship total = %d, want exact average-four total %d", relationships, want)
	}
}

func TestFormalLoginAndRelationshipPhasesComposeToExactMillion(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		runID string
		seed  uint64
	}{
		{name: "default", runID: FormalConfig().RunID, seed: FormalConfig().Seed},
		{name: "opposite_rounding", runID: "phase-repro", seed: 40},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := FormalConfig()
			cfg.RunID = testCase.runID
			cfg.Seed = testCase.seed
			identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
			if err != nil {
				t.Fatalf("NewIdentitySpace: %v", err)
			}
			schedule, err := NewScheduleModel(identity, cfg.Workload)
			if err != nil {
				t.Fatalf("NewScheduleModel: %v", err)
			}
			graph, err := NewRelationshipGraph(identity)
			if err != nil {
				t.Fatalf("NewRelationshipGraph: %v", err)
			}

			var localNew [formalWorkers]uint64
			var relationships uint64
			for loginOrdinal := uint64(0); loginOrdinal < 312_500; loginOrdinal++ {
				login, loginErr := schedule.Login(loginOrdinal)
				if loginErr != nil {
					t.Fatalf("Login(%d): %v", loginOrdinal, loginErr)
				}
				if login.Identity != LoginNew {
					continue
				}
				workerID := loginOrdinal % uint64(cfg.Workload.Workers)
				ownerIndex, indexErr := identity.GlobalIndex(workerID, localNew[workerID])
				if indexErr != nil {
					t.Fatalf("GlobalIndex(%d, %d): %v", workerID, localNew[workerID], indexErr)
				}
				localNew[workerID]++
				outgoing, outgoingErr := graph.OutgoingForOrdinal(ownerIndex, login.NewOrdinal)
				if outgoingErr != nil {
					t.Fatalf("Outgoing(%d): %v", ownerIndex, outgoingErr)
				}
				relationships += uint64(outgoing.Count)
				for edgeIndex := 0; edgeIndex < outgoing.Count; edgeIndex++ {
					ownerWorker, _ := identity.Owner(outgoing.Items[edgeIndex].OwnerIndex)
					peerWorker, _ := identity.Owner(outgoing.Items[edgeIndex].PeerIndex)
					if ownerWorker != workerID || peerWorker != workerID {
						t.Fatalf("login %d relationship crosses workers: %+v", loginOrdinal, outgoing.Items[edgeIndex])
					}
				}
			}
			if localNew != [formalWorkers]uint64{83_334, 83_334, 83_332} && testCase.name == "default" {
				t.Fatalf("default formal new-user lanes = %v", localNew)
			}
			if relationships != 1_000_000 {
				t.Fatalf("formal relationships = %d, want 1000000 (login_phase=%d degree_phase=%d lanes=%v)", relationships, schedule.loginPhase, graph.degreePhase, localNew)
			}
		})
	}
}

func TestEveryLoginAndDegreePhaseComposesToExactFormalRelationshipTotal(t *testing.T) {
	cfg := FormalConfig()
	identity, err := NewIdentitySpace("relationship-phase-matrix", 71, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace: %v", err)
	}
	schedule, err := NewScheduleModel(identity, cfg.Workload)
	if err != nil {
		t.Fatalf("NewScheduleModel: %v", err)
	}
	graph, err := NewRelationshipGraph(identity)
	if err != nil {
		t.Fatalf("NewRelationshipGraph: %v", err)
	}

	for loginPhase := uint64(0); loginPhase < distributionCycle; loginPhase++ {
		schedule.loginPhase = loginPhase
		newUsers := schedule.NewOrdinalBefore(312_500)
		if newUsers != 250_000 {
			t.Fatalf("login phase %d new users = %d, want 250000", loginPhase, newUsers)
		}
		for degreePhase := uint64(0); degreePhase < uint64(len(relationshipDegreePattern)); degreePhase++ {
			graph.degreePhase = degreePhase
			var cycleRelationships uint64
			for newOrdinal := uint64(0); newOrdinal < uint64(len(relationshipDegreePattern)); newOrdinal++ {
				cycleRelationships += uint64(graph.Degree(newOrdinal))
			}
			relationships := (newUsers / uint64(len(relationshipDegreePattern))) * cycleRelationships
			for newOrdinal := newUsers - newUsers%uint64(len(relationshipDegreePattern)); newOrdinal < newUsers; newOrdinal++ {
				relationships += uint64(graph.Degree(newOrdinal))
			}
			if relationships != 1_000_000 {
				t.Fatalf("login phase %d degree phase %d relationships = %d, want 1000000", loginPhase, degreePhase, relationships)
			}
		}
	}
}

func TestRelationshipGraphReconstructsIncomingEdgesFromPreviousFiveOwners(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	for user := uint64(100); user < 2_000; user++ {
		incoming := graph.Incoming(user)
		var wantOwners [MaxForwardRelationships]uint64
		wantCount := 0
		for distance := uint64(1); distance <= MaxForwardRelationships; distance++ {
			owner := user - distance
			outgoing, err := graph.Outgoing(owner)
			if err != nil {
				t.Fatalf("Outgoing(%d) error = %v", owner, err)
			}
			for edgeIndex := 0; edgeIndex < outgoing.Count; edgeIndex++ {
				if outgoing.Items[edgeIndex].PeerIndex == user {
					wantOwners[wantCount] = owner
					wantCount++
				}
			}
		}
		if incoming.Count != wantCount {
			t.Fatalf("Incoming(%d).Count = %d, want %d", user, incoming.Count, wantCount)
		}
		for edgeIndex := 0; edgeIndex < incoming.Count; edgeIndex++ {
			if incoming.Items[edgeIndex].OwnerIndex != wantOwners[edgeIndex] || incoming.Items[edgeIndex].PeerIndex != user {
				t.Fatalf("Incoming(%d)[%d] = %d -> %d, want %d -> %d", user, edgeIndex, incoming.Items[edgeIndex].OwnerIndex, incoming.Items[edgeIndex].PeerIndex, wantOwners[edgeIndex], user)
			}
		}
	}
}

func TestRelationshipGraphMatureUsersHaveBoundedConversationDegree(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	for user := uint64(5); user < 100_005; user++ {
		incoming := graph.Incoming(user)
		degree := incoming.Count + int(graph.Degree(user))
		if incoming.Count < 3 || incoming.Count > 5 || degree < 6 || degree > MaxUserRelationships {
			t.Fatalf("user %d incoming/total degree = %d/%d, want 3..5/6..10", user, incoming.Count, degree)
		}
	}
}

func TestRelationshipGraphUsesCanonicalPersonChannelAndKeepsPeerUID(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	edges, err := graph.Outgoing(50)
	if err != nil {
		t.Fatalf("Outgoing() error = %v", err)
	}
	edge := edges.Items[0]

	forward, err := channelid.NormalizePersonChannel(edge.OwnerUID, edge.PeerUID)
	if err != nil {
		t.Fatalf("NormalizePersonChannel(forward) error = %v", err)
	}
	reverse, err := channelid.NormalizePersonChannel(edge.PeerUID, edge.OwnerUID)
	if err != nil {
		t.Fatalf("NormalizePersonChannel(reverse) error = %v", err)
	}
	if edge.PersonChannelID != forward || forward != reverse {
		t.Fatalf("person channel = %q, forward/reverse = %q/%q", edge.PersonChannelID, forward, reverse)
	}
	peerIndex, peerUID, ok := edge.PeerFor(edge.OwnerIndex)
	if !ok || peerIndex != edge.PeerIndex || peerUID != edge.PeerUID || peerUID == edge.PersonChannelID {
		t.Fatalf("PeerFor(owner) = %d/%q/%v; channel = %q", peerIndex, peerUID, ok, edge.PersonChannelID)
	}
}

func TestRelationshipGraphRejectsForwardIndexOverflow(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	if _, err := graph.Outgoing(math.MaxUint64); err == nil {
		t.Fatal("Outgoing(MaxUint64) error = nil")
	}
}

func TestReturningCandidateIsUnavailableWithoutMatureHistory(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	for _, nextNewIndex := range []uint64{0, 10} {
		candidate, err := graph.ReturningCandidate(nextNewIndex, 0, 250_000)
		if err != nil {
			t.Fatalf("ReturningCandidate(%d) error = %v", nextNewIndex, err)
		}
		if candidate.Available {
			t.Fatalf("ReturningCandidate(%d) = %+v, want unavailable", nextNewIndex, candidate)
		}
	}
	if _, err := graph.ReturningCandidate(100, 0, 0); !errors.Is(err, errReturningHistoryWindow) {
		t.Fatalf("ReturningCandidate(zero new users per day) error = %v, want %v", err, errReturningHistoryWindow)
	}
}

func TestReturningCandidateFallsBackFromOlderToRecentBeforeFirstDay(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	const nextNewIndex = uint64(1_000)
	const newUsersPerDay = uint64(250_000)

	var candidate ReturningCandidate
	for ordinal := uint64(0); ordinal < 5; ordinal++ {
		got, err := graph.ReturningCandidate(nextNewIndex, ordinal, newUsersPerDay)
		if err != nil {
			t.Fatalf("ReturningCandidate(%d) error = %v", ordinal, err)
		}
		if got.PreferredBucket == HistoryOlder {
			candidate = got
			break
		}
	}
	if !candidate.Available {
		t.Fatal("complete five-ordinal cycle had no available older-preference candidate")
	}
	if candidate.ActualBucket != HistoryRecent || !candidate.Fallback {
		t.Fatalf("preferred/actual/fallback = %q/%q/%v, want older/recent/true", candidate.PreferredBucket, candidate.ActualBucket, candidate.Fallback)
	}
	if candidate.UserIndex < 5 || candidate.UserIndex+5 >= nextNewIndex {
		t.Fatalf("candidate user %d is not mature below high-water %d", candidate.UserIndex, nextNewIndex)
	}
}

func TestReturningCandidateUsesUnbiasedCandidateRangeSampling(t *testing.T) {
	graph, _ := newTestRelationshipGraph(t)
	newUsersPerDay := uint64(1<<63) + 6
	nextNewIndex := newUsersPerDay + 100

	candidate, err := graph.ReturningCandidate(nextNewIndex, 1, newUsersPerDay)
	if err != nil {
		t.Fatalf("ReturningCandidate() error = %v", err)
	}
	if candidate.PreferredBucket != HistoryRecent || candidate.ActualBucket != HistoryRecent || candidate.Fallback {
		t.Fatalf("candidate bucket preferred/actual/fallback = %q/%q/%v, want recent/recent/false", candidate.PreferredBucket, candidate.ActualBucket, candidate.Fallback)
	}
	const wantUserIndex = uint64(4_875_739_668_305_399_338)
	if candidate.UserIndex != wantUserIndex {
		t.Fatalf("candidate user index = %d, want unbiased rejection sample %d", candidate.UserIndex, wantUserIndex)
	}
}

func TestReturningCandidateHasExactMatureBucketCyclesAndRealDistinctEdges(t *testing.T) {
	graph, identity := newTestRelationshipGraph(t)
	const nextNewIndex = uint64(1_000_011)
	const newUsersPerDay = uint64(250_000)
	boundary := nextNewIndex - newUsersPerDay
	var preferredRecent, preferredOlder int
	var actualRecent, actualOlder int
	var oneConversation, twoConversations int

	for ordinal := uint64(0); ordinal < 1_000; ordinal++ {
		candidate, err := graph.ReturningCandidate(nextNewIndex, ordinal, newUsersPerDay)
		if err != nil {
			t.Fatalf("ReturningCandidate(%d) error = %v", ordinal, err)
		}
		if !candidate.Available || candidate.Fallback {
			t.Fatalf("ReturningCandidate(%d) available/fallback = %v/%v", ordinal, candidate.Available, candidate.Fallback)
		}
		switch candidate.PreferredBucket {
		case HistoryRecent:
			preferredRecent++
		case HistoryOlder:
			preferredOlder++
		default:
			t.Fatalf("ReturningCandidate(%d) preferred bucket = %q", ordinal, candidate.PreferredBucket)
		}
		switch candidate.ActualBucket {
		case HistoryRecent:
			actualRecent++
			if candidate.UserIndex < boundary {
				t.Fatalf("recent candidate user %d precedes boundary %d", candidate.UserIndex, boundary)
			}
		case HistoryOlder:
			actualOlder++
			if candidate.UserIndex >= boundary {
				t.Fatalf("older candidate user %d reaches boundary %d", candidate.UserIndex, boundary)
			}
		default:
			t.Fatalf("ReturningCandidate(%d) actual bucket = %q", ordinal, candidate.ActualBucket)
		}
		if candidate.UserIndex < 5 || candidate.UserIndex+5 >= nextNewIndex {
			t.Fatalf("candidate user %d is not mature below %d", candidate.UserIndex, nextNewIndex)
		}
		if candidate.UserUID != identity.UID(candidate.UserIndex) {
			t.Fatalf("candidate UID %q does not match user %d", candidate.UserUID, candidate.UserIndex)
		}
		if candidate.ConversationCount < 1 || candidate.ConversationCount > 2 {
			t.Fatalf("conversation count = %d, want 1..2", candidate.ConversationCount)
		}
		if candidate.ConversationCount == 1 {
			oneConversation++
		} else {
			twoConversations++
			if candidate.Conversations[0].PeerIndex == candidate.Conversations[1].PeerIndex {
				t.Fatalf("candidate %d repeats peer %d", candidate.UserIndex, candidate.Conversations[0].PeerIndex)
			}
		}
		for conversationIndex := 0; conversationIndex < candidate.ConversationCount; conversationIndex++ {
			conversation := candidate.Conversations[conversationIndex]
			if conversation.PeerUID != identity.UID(conversation.PeerIndex) {
				t.Fatalf("conversation peer UID %q does not match index %d", conversation.PeerUID, conversation.PeerIndex)
			}
			if conversation.AvailableAtIndex >= nextNewIndex {
				t.Fatalf("conversation availability %d reaches high-water %d", conversation.AvailableAtIndex, nextNewIndex)
			}
			if candidate.ActualBucket == HistoryOlder && conversation.AvailableAtIndex >= boundary {
				t.Fatalf("older conversation availability %d reaches boundary %d", conversation.AvailableAtIndex, boundary)
			}
			if candidate.ActualBucket == HistoryRecent && conversation.AvailableAtIndex < boundary {
				t.Fatalf("recent conversation availability %d precedes boundary %d", conversation.AvailableAtIndex, boundary)
			}
			assertRealAdjacentConversation(t, graph, candidate.UserIndex, conversation)
		}
	}

	if preferredRecent != 800 || preferredOlder != 200 || actualRecent != 800 || actualOlder != 200 {
		t.Fatalf("preferred recent/older actual recent/older = %d/%d %d/%d, want 800/200 800/200", preferredRecent, preferredOlder, actualRecent, actualOlder)
	}
	if oneConversation == 0 || twoConversations == 0 {
		t.Fatalf("one/two conversation selections = %d/%d, want both represented", oneConversation, twoConversations)
	}
}

func TestRelationshipGraphDecisionsAreIndependentOfReturningDraws(t *testing.T) {
	graph, identity := newTestRelationshipGraph(t)
	wantDegree := graph.Degree(12_345)
	wantUID := identity.UID(12_345)
	for ordinal := uint64(0); ordinal < 100; ordinal++ {
		_, _ = graph.ReturningCandidate(500_000, ordinal, 250_000)
	}
	if got := graph.Degree(12_345); got != wantDegree {
		t.Fatalf("Degree() shifted from %d to %d", wantDegree, got)
	}
	if got := identity.UID(12_345); got != wantUID {
		t.Fatalf("UID() shifted from %q to %q", wantUID, got)
	}
}

func assertRealAdjacentConversation(t *testing.T, graph RelationshipGraph, userIndex uint64, conversation ReturningConversation) {
	t.Helper()
	incoming := graph.Incoming(userIndex)
	for edgeIndex := 0; edgeIndex < incoming.Count; edgeIndex++ {
		edge := incoming.Items[edgeIndex]
		peerIndex, peerUID, _ := edge.PeerFor(userIndex)
		if peerIndex == conversation.PeerIndex && peerUID == conversation.PeerUID && edge.PersonChannelID == conversation.PersonChannelID && edge.AvailableAtIndex == conversation.AvailableAtIndex {
			return
		}
	}
	outgoing, err := graph.Outgoing(userIndex)
	if err != nil {
		t.Fatalf("Outgoing(%d) error = %v", userIndex, err)
	}
	for edgeIndex := 0; edgeIndex < outgoing.Count; edgeIndex++ {
		edge := outgoing.Items[edgeIndex]
		peerIndex, peerUID, _ := edge.PeerFor(userIndex)
		if peerIndex == conversation.PeerIndex && peerUID == conversation.PeerUID && edge.PersonChannelID == conversation.PersonChannelID && edge.AvailableAtIndex == conversation.AvailableAtIndex {
			return
		}
	}
	t.Fatalf("conversation %+v is not adjacent to user %d", conversation, userIndex)
}
