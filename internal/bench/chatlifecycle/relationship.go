package chatlifecycle

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

const (
	// MaxForwardRelationships bounds the owner-created forward edge set.
	MaxForwardRelationships = 5
	// MaxUserRelationships bounds incoming plus outgoing person conversations.
	MaxUserRelationships = 2 * MaxForwardRelationships
)

var relationshipDegreePattern = [4]uint8{3, 4, 4, 5}

var (
	errRelationshipIdentityRequired = errors.New("chat lifecycle relationship: identity space is required")
	errReturningHistoryWindow       = errors.New("chat lifecycle relationship: new users per day must cover at least six indexes")
)

// HistoryBucket classifies a candidate and its selected edge availability
// relative to the preceding-new-user-day boundary.
type HistoryBucket string

const (
	// HistoryRecent means the candidate and selected edges are inside the preceding day.
	HistoryRecent HistoryBucket = "recent"
	// HistoryOlder means the candidate and selected edges precede the preceding day.
	HistoryOlder HistoryBucket = "older"
)

// RelationshipEdge is a canonical lower-to-higher person relationship. The
// edge becomes real only when AvailableAtIndex, the higher endpoint, arrives.
type RelationshipEdge struct {
	OwnerIndex       uint64
	OwnerUID         string
	PeerIndex        uint64
	PeerUID          string
	PersonChannelID  string
	AvailableAtIndex uint64
}

// PeerFor returns the other endpoint separately from the canonical channel ID,
// allowing later workers to address a real peer UID in SEND frames.
func (e RelationshipEdge) PeerFor(userIndex uint64) (peerIndex uint64, peerUID string, ok bool) {
	switch userIndex {
	case e.OwnerIndex:
		return e.PeerIndex, e.PeerUID, true
	case e.PeerIndex:
		return e.OwnerIndex, e.OwnerUID, true
	default:
		return 0, "", false
	}
}

// ForwardRelationshipSet is a fixed-capacity result for outgoing or incoming
// reconstruction; Count identifies the populated prefix of Items.
type ForwardRelationshipSet struct {
	Items [MaxForwardRelationships]RelationshipEdge
	Count int
}

// UserRelationshipSet is a fixed-capacity result containing all currently
// available incoming and outgoing relationships for one user.
type UserRelationshipSet struct {
	Items [MaxUserRelationships]RelationshipEdge
	Count int
}

// ReturningConversation identifies one real adjacent edge from the selected
// user's perspective and keeps the peer UID separate from the channel ID.
type ReturningConversation struct {
	PeerIndex       uint64
	PeerUID         string
	PersonChannelID string
	// AvailableAtIndex is the higher endpoint whose arrival made the edge real.
	AvailableAtIndex uint64
}

// ReturningCandidate is a deterministic historical login candidate. Available
// does not claim that the user is offline; later worker logic owns that check.
type ReturningCandidate struct {
	Available         bool
	UserIndex         uint64
	UserUID           string
	PreferredBucket   HistoryBucket
	ActualBucket      HistoryBucket
	Fallback          bool
	Conversations     [2]ReturningConversation
	ConversationCount int
}

// RelationshipGraph reconstructs a sparse bounded-degree graph from indexes;
// it retains no adjacency list or historical identity map.
type RelationshipGraph struct {
	identity    *IdentitySpace
	degreePhase uint64
}

// NewRelationshipGraph binds graph decisions to a validated identity space and
// rejects nil rather than constructing a graph that would panic on first use.
func NewRelationshipGraph(identity *IdentitySpace) (RelationshipGraph, error) {
	if identity == nil {
		return RelationshipGraph{}, errRelationshipIdentityRequired
	}
	return RelationshipGraph{
		identity:    identity,
		degreePhase: identity.decisionUint64("relationship-degree-phase/v1") % uint64(len(relationshipDegreePattern)),
	}, nil
}

// Degree returns the exact repeating 3/4/4/5 forward degree, rotated by a
// run-seeded phase without changing the distribution of any four-index block.
func (g RelationshipGraph) Degree(ownerIndex uint64) uint8 {
	phase := (ownerIndex%uint64(len(relationshipDegreePattern)) + g.degreePhase) % uint64(len(relationshipDegreePattern))
	return relationshipDegreePattern[phase]
}

// Outgoing reconstructs all unique forward edges owned by ownerIndex.
func (g RelationshipGraph) Outgoing(ownerIndex uint64) (ForwardRelationshipSet, error) {
	var result ForwardRelationshipSet
	degree := int(g.Degree(ownerIndex))
	for offset := 1; offset <= degree; offset++ {
		peerIndex, err := checkedAddIndex(ownerIndex, uint64(offset))
		if err != nil {
			return ForwardRelationshipSet{}, err
		}
		result.Items[result.Count] = g.edge(ownerIndex, peerIndex)
		result.Count++
	}
	return result, nil
}

// Incoming reconstructs edges from at most the previous five owner indexes.
func (g RelationshipGraph) Incoming(userIndex uint64) ForwardRelationshipSet {
	var result ForwardRelationshipSet
	for distance := uint64(1); distance <= MaxForwardRelationships && distance <= userIndex; distance++ {
		ownerIndex := userIndex - distance
		if uint64(g.Degree(ownerIndex)) < distance {
			continue
		}
		result.Items[result.Count] = g.edge(ownerIndex, userIndex)
		result.Count++
	}
	return result
}

// AvailableRelationships reconstructs only real edges whose higher endpoint
// is below nextNewIndex. Its fixed result cannot grow with run history.
func (g RelationshipGraph) AvailableRelationships(userIndex, nextNewIndex uint64) (UserRelationshipSet, error) {
	var result UserRelationshipSet
	incoming := g.Incoming(userIndex)
	for edgeIndex := 0; edgeIndex < incoming.Count; edgeIndex++ {
		edge := incoming.Items[edgeIndex]
		if edge.AvailableAtIndex < nextNewIndex {
			result.Items[result.Count] = edge
			result.Count++
		}
	}
	outgoing, err := g.Outgoing(userIndex)
	if err != nil {
		return UserRelationshipSet{}, err
	}
	for edgeIndex := 0; edgeIndex < outgoing.Count; edgeIndex++ {
		edge := outgoing.Items[edgeIndex]
		if edge.AvailableAtIndex < nextNewIndex {
			result.Items[result.Count] = edge
			result.Count++
		}
	}
	return result, nil
}

// ReturningCandidate selects a mature historical user plus one or two real
// adjacent conversations. Preference follows an exact seeded four-recent,
// one-older cycle; unavailable older history explicitly falls back to recent.
func (g RelationshipGraph) ReturningCandidate(nextNewIndex, loginOrdinal, newUsersPerDay uint64) (ReturningCandidate, error) {
	if newUsersPerDay < MaxForwardRelationships+1 {
		return ReturningCandidate{}, errReturningHistoryWindow
	}
	preferencePhase, err := g.identity.decisionBelow("returning-history-bucket-phase/v1", 5)
	if err != nil {
		return ReturningCandidate{}, err
	}
	preferredBucket := HistoryRecent
	if (loginOrdinal%5+preferencePhase)%5 == 4 {
		preferredBucket = HistoryOlder
	}
	result := ReturningCandidate{PreferredBucket: preferredBucket}

	recent, older := returningCandidateRanges(nextNewIndex, newUsersPerDay)
	selectedRange, actualBucket, available := recent, HistoryRecent, recent.available
	if preferredBucket == HistoryOlder {
		selectedRange, actualBucket, available = older, HistoryOlder, older.available
	}
	if !available {
		fallbackRange, fallbackBucket := older, HistoryOlder
		if preferredBucket == HistoryOlder {
			fallbackRange, fallbackBucket = recent, HistoryRecent
		}
		if !fallbackRange.available {
			return result, nil
		}
		selectedRange, actualBucket = fallbackRange, fallbackBucket
		result.Fallback = true
	}

	span := selectedRange.max - selectedRange.min + 1
	draw, err := g.identity.decisionBelow("returning-candidate-index/v1", span, nextNewIndex, loginOrdinal, newUsersPerDay, uint64(historyBucketCode(actualBucket)))
	if err != nil {
		return ReturningCandidate{}, err
	}
	userIndex := selectedRange.min + draw
	relationships, err := g.AvailableRelationships(userIndex, nextNewIndex)
	if err != nil {
		return ReturningCandidate{}, err
	}
	if relationships.Count == 0 {
		return result, nil
	}

	result.Available = true
	result.UserIndex = userIndex
	result.UserUID = g.identity.UID(userIndex)
	result.ActualBucket = actualBucket
	result.ConversationCount = 1 + int(g.identity.decisionUint64("returning-conversation-count/v1", nextNewIndex, loginOrdinal, userIndex)%2)
	firstDraw, err := g.identity.decisionBelow("returning-conversation-first/v1", uint64(relationships.Count), nextNewIndex, loginOrdinal, userIndex)
	if err != nil {
		return ReturningCandidate{}, err
	}
	first := int(firstDraw)
	result.Conversations[0] = returningConversationFor(relationships.Items[first], userIndex)
	if result.ConversationCount == 2 {
		offsetDraw, err := g.identity.decisionBelow("returning-conversation-second-offset/v1", uint64(relationships.Count-1), nextNewIndex, loginOrdinal, userIndex)
		if err != nil {
			return ReturningCandidate{}, err
		}
		offset := 1 + int(offsetDraw)
		second := first + offset
		if second >= relationships.Count {
			second -= relationships.Count
		}
		result.Conversations[1] = returningConversationFor(relationships.Items[second], userIndex)
	}
	return result, nil
}

type candidateRange struct {
	min       uint64
	max       uint64
	available bool
}

func returningCandidateRanges(nextNewIndex, newUsersPerDay uint64) (recent, older candidateRange) {
	if nextNewIndex <= 2*MaxForwardRelationships {
		return candidateRange{}, candidateRange{}
	}
	boundary := uint64(0)
	if nextNewIndex > newUsersPerDay {
		boundary = nextNewIndex - newUsersPerDay
	}
	maxMature := nextNewIndex - MaxForwardRelationships - 1
	recentMin := boundary
	if recentMin < MaxForwardRelationships {
		recentMin = MaxForwardRelationships
	}
	if recentMin <= maxMature {
		recent = candidateRange{min: recentMin, max: maxMature, available: true}
	}
	if boundary > 2*MaxForwardRelationships {
		older = candidateRange{min: MaxForwardRelationships, max: boundary - MaxForwardRelationships - 1, available: true}
	}
	return recent, older
}

func historyBucketCode(bucket HistoryBucket) uint8 {
	if bucket == HistoryOlder {
		return 1
	}
	return 0
}

func returningConversationFor(edge RelationshipEdge, userIndex uint64) ReturningConversation {
	peerIndex, peerUID, _ := edge.PeerFor(userIndex)
	return ReturningConversation{
		PeerIndex:        peerIndex,
		PeerUID:          peerUID,
		PersonChannelID:  edge.PersonChannelID,
		AvailableAtIndex: edge.AvailableAtIndex,
	}
}

func (g RelationshipGraph) edge(ownerIndex, peerIndex uint64) RelationshipEdge {
	ownerUID := g.identity.UID(ownerIndex)
	peerUID := g.identity.UID(peerIndex)
	personChannelID, _ := channelid.NormalizePersonChannel(ownerUID, peerUID)
	return RelationshipEdge{
		OwnerIndex:       ownerIndex,
		OwnerUID:         ownerUID,
		PeerIndex:        peerIndex,
		PeerUID:          peerUID,
		PersonChannelID:  personChannelID,
		AvailableAtIndex: peerIndex,
	}
}
