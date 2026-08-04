package chatlifecycle

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	groupIDPrefix        = "wkg-"
	maxGroupCatalogCount = 2_000
	maxGroupMembers      = 100_000
)

var (
	errGroupIdentityRequired = errors.New("chat lifecycle groups: identity space is required")
	errGroupCatalog          = errors.New("chat lifecycle groups: fixed catalog must contain at most 2000 groups and one optional very-large group")
	errGroupPrimaryClasses   = errors.New("chat lifecycle groups: at least one small, medium, or large primary class is required")
	errGroupIndex            = errors.New("chat lifecycle groups: group index is outside the catalog")
	errGroupMember           = errors.New("chat lifecycle groups: member ordinal is outside the group")
	errGroupMemberOverflow   = errors.New("chat lifecycle groups: member identity index overflows uint64")
	errGroupHotSet           = errors.New("chat lifecycle groups: person hot-set count is invalid")
	errGroupCanary           = errors.New("chat lifecycle groups: very-large canary is not configured")
)

// GroupCategory is one fixed membership-size class.
type GroupCategory uint8

const (
	// GroupSmall has 5..20 members.
	GroupSmall GroupCategory = iota + 1
	// GroupMedium has 100..500 members.
	GroupMedium
	// GroupLarge has 1,000..10,000 members.
	GroupLarge
	// GroupVeryLarge is the separate correctness-canary group.
	GroupVeryLarge
)

// Group is one reconstructed fixed catalog entry. It holds only one base
// index, never a member slice, even for the 100,000-member canary.
type Group struct {
	Index        uint64
	ID           string
	Category     GroupCategory
	MemberCount  int
	identity     *IdentitySpace
	memberBase   uint64
	memberStride uint64
}

// GroupReturningMember is one fixed-roster identity selected for a historical
// returning login. Consecutive ordinals form a same-group pair so sampled
// delivery can require a distinct online recipient without synthetic users.
type GroupReturningMember struct {
	Group         Group
	MemberOrdinal int
	UserIndex     uint64
}

// MemberUID reconstructs one unique member in O(1) memory.
func (g Group) MemberUID(memberOrdinal int) (string, error) {
	index, err := g.MemberIndex(memberOrdinal)
	if err != nil {
		return "", err
	}
	return g.identity.UID(index), nil
}

// MemberIndex reconstructs one fixed member without formatting its UID.
func (g Group) MemberIndex(memberOrdinal int) (uint64, error) {
	if memberOrdinal < 0 || memberOrdinal >= g.MemberCount || g.identity == nil {
		return 0, errGroupMember
	}
	if uint64(memberOrdinal) > math.MaxUint64/g.memberStride {
		return 0, errGroupMemberOverflow
	}
	return checkedGroupMemberIndex(g.memberBase, uint64(memberOrdinal)*g.memberStride)
}

// ContainsIndex checks fixed strided membership in O(1) memory.
func (g Group) ContainsIndex(userIndex uint64) bool {
	if g.identity == nil || g.memberStride == 0 || userIndex < g.memberBase {
		return false
	}
	delta := userIndex - g.memberBase
	return delta%g.memberStride == 0 && delta/g.memberStride < uint64(g.MemberCount)
}

// GroupCanary is the independent very-large-group correctness stream. It is
// excluded from primary rate and traffic-share denominators.
type GroupCanary struct {
	Group   Group
	Ordinal uint64
	Every   time.Duration
}

// GroupHotSet makes explicit that the fixed group catalog contributes no
// historical channel growth.
type GroupHotSet struct {
	PersonChannels        int
	GroupChannels         int
	TotalChannels         int
	HistoricalGroupGrowth int
}

// GroupCatalog reconstructs fixed groups and membership from run identity and
// indexes. Its retained state is bounded by four category counters.
type GroupCatalog struct {
	identity       *IdentitySpace
	counts         [4]int
	starts         [4]uint64
	total          int
	veryLargeCount int
	veryLargeEvery time.Duration
	primaryWeight  [3]int
	primaryTotal   uint64
	primaryPhase   uint64
}

// NewGroupCatalog validates and copies a fixed catalog. Missing primary
// classes are deterministically omitted and the remaining 80/15/5 weights are
// normalized; the very-large class is never included in primary selection.
func NewGroupCatalog(identity *IdentitySpace, config GroupCatalogConfig) (GroupCatalog, error) {
	if identity == nil {
		return GroupCatalog{}, errGroupIdentityRequired
	}
	counts := [4]int{config.Small, config.Medium, config.Large, config.VeryLarge}
	total := 0
	for _, count := range counts {
		if count < 0 || count > maxGroupCatalogCount || total > maxGroupCatalogCount-count {
			return GroupCatalog{}, errGroupCatalog
		}
		total += count
	}
	if total <= 0 || total > maxGroupCatalogCount || !config.FixedMembership || config.VeryLarge < 0 || config.VeryLarge > 1 {
		return GroupCatalog{}, errGroupCatalog
	}
	if config.VeryLarge == 0 {
		if config.VeryLargeMembers != 0 || config.VeryLargeSendEvery != 0 {
			return GroupCatalog{}, errGroupCatalog
		}
	} else if config.VeryLargeMembers < 5 || config.VeryLargeMembers > maxGroupMembers || config.VeryLargeSendEvery != time.Minute {
		return GroupCatalog{}, errGroupCatalog
	}
	primaryWeight := [3]int{}
	availableWeights := [3]int{80, 15, 5}
	primaryTotal := 0
	for category := 0; category < len(primaryWeight); category++ {
		if counts[category] > 0 {
			primaryWeight[category] = availableWeights[category]
			primaryTotal += availableWeights[category]
		}
	}
	if primaryTotal == 0 {
		return GroupCatalog{}, errGroupPrimaryClasses
	}
	phase, err := identity.decisionBelow("primary-group-class-ordinal-phase/v1", uint64(primaryTotal))
	if err != nil {
		return GroupCatalog{}, err
	}
	catalog := GroupCatalog{
		identity:       identity,
		counts:         counts,
		total:          total,
		veryLargeCount: config.VeryLargeMembers,
		veryLargeEvery: config.VeryLargeSendEvery,
		primaryWeight:  primaryWeight,
		primaryTotal:   uint64(primaryTotal),
		primaryPhase:   phase,
	}
	var start uint64
	for category, count := range counts {
		catalog.starts[category] = start
		start += uint64(count)
	}
	return catalog, nil
}

// Count returns the fixed number of group channels.
func (c GroupCatalog) Count() int { return c.total }

// GroupForMemberIndex reverses the fixed strided roster without scanning the
// catalog. One identity can belong to at most one group in this layout.
func (c GroupCatalog) GroupForMemberIndex(userIndex uint64) (Group, int, bool, error) {
	if c.total <= 0 {
		return Group{}, 0, false, errGroupCatalog
	}
	groupIndex := userIndex % uint64(c.total)
	group, err := c.Group(groupIndex)
	if err != nil {
		return Group{}, 0, false, err
	}
	memberOrdinal := userIndex / uint64(c.total)
	if memberOrdinal >= uint64(group.MemberCount) {
		return Group{}, 0, false, nil
	}
	return group, int(memberOrdinal), true, nil
}

// Group reconstructs one catalog entry and a checked strided membership base.
// The catalog index itself is member zero, guaranteeing that the fixed
// prepared roster intersects the initial bounded online population; later
// members remain spread across deterministic arrival cohorts.
func (c GroupCatalog) Group(index uint64) (Group, error) {
	if index >= uint64(c.total) {
		return Group{}, errGroupIndex
	}
	categoryIndex := c.categoryIndex(index)
	memberCount, err := c.memberCount(index, categoryIndex)
	if err != nil {
		return Group{}, err
	}
	return Group{
		Index:        index,
		ID:           groupIDPrefix + c.identity.namespace + "-" + strconv.FormatUint(index, 36),
		Category:     GroupCategory(categoryIndex + 1),
		MemberCount:  memberCount,
		identity:     c.identity,
		memberBase:   index,
		memberStride: uint64(c.total),
	}, nil
}

// IndexFromGroupID reverses only IDs in this run's bounded namespace.
func (c GroupCatalog) IndexFromGroupID(groupID string) (uint64, bool) {
	prefix := groupIDPrefix + c.identity.namespace + "-"
	if !strings.HasPrefix(groupID, prefix) || len(groupID) == len(prefix) {
		return 0, false
	}
	suffix := groupID[len(prefix):]
	index, err := strconv.ParseUint(suffix, 36, 64)
	return index, err == nil && index < uint64(c.total) && suffix == strconv.FormatUint(index, 36)
}

// PrimaryTarget returns the exact 80/15/5 category cycle when all primary
// classes exist. Missing local classes are omitted and available weights are
// normalized without ever selecting the very-large canary.
func (c GroupCatalog) PrimaryTarget(logicalOrdinal uint64) (Group, error) {
	position := (logicalOrdinal%c.primaryTotal + c.primaryPhase) % c.primaryTotal
	boundary := uint64(0)
	categoryIndex := -1
	for candidate, weight := range c.primaryWeight {
		boundary += uint64(weight)
		if position < boundary {
			categoryIndex = candidate
			break
		}
	}
	if categoryIndex < 0 || c.counts[categoryIndex] == 0 {
		return Group{}, errGroupPrimaryClasses
	}
	offset, err := c.identity.decisionBelow(
		"primary-group-index/v1",
		uint64(c.counts[categoryIndex]),
		logicalOrdinal,
		uint64(categoryIndex),
	)
	if err != nil {
		return Group{}, err
	}
	return c.Group(c.starts[categoryIndex] + offset)
}

// VeryLargeCanary returns the one separately scheduled correctness target.
func (c GroupCatalog) VeryLargeCanary(ordinal uint64) (GroupCanary, error) {
	if c.counts[3] != 1 || c.veryLargeEvery <= 0 {
		return GroupCanary{}, errGroupCanary
	}
	group, err := c.Group(c.starts[3])
	if err != nil {
		return GroupCanary{}, err
	}
	return GroupCanary{Group: group, Ordinal: ordinal, Every: c.veryLargeEvery}, nil
}

// ReturningMember selects fixed members from one historical index range in
// pairs, then rotates to the next group in the requested category. Member zero
// is deliberately excluded because it only proves initial-roster reachability.
func (c GroupCatalog) ReturningMember(category GroupCategory, categoryOrdinal, minimum, maximum uint64) (GroupReturningMember, bool, error) {
	categoryIndex := int(category) - 1
	if categoryIndex < 0 || categoryIndex >= len(c.counts) || c.counts[categoryIndex] == 0 || minimum > maximum {
		return GroupReturningMember{}, false, nil
	}
	count := uint64(c.counts[categoryIndex])
	groupOffset := (categoryOrdinal / 2) % count
	group, err := c.Group(c.starts[categoryIndex] + groupOffset)
	if err != nil {
		return GroupReturningMember{}, false, err
	}
	first, last, ok := group.memberOrdinalsInRange(minimum, maximum)
	if !ok {
		return GroupReturningMember{}, false, nil
	}
	span := last - first + 1
	round := categoryOrdinal / (2 * count)
	memberOffset := ((round%span)*2 + categoryOrdinal%2) % span
	memberOrdinal := first + memberOffset
	userIndex, err := group.MemberIndex(int(memberOrdinal))
	if err != nil {
		return GroupReturningMember{}, false, err
	}
	return GroupReturningMember{Group: group, MemberOrdinal: int(memberOrdinal), UserIndex: userIndex}, true, nil
}

// HotSet combines active person channels with this fixed catalog and records
// zero historical group growth.
func (c GroupCatalog) HotSet(personChannels int) (GroupHotSet, error) {
	if personChannels <= 0 || personChannels > math.MaxInt-c.total {
		return GroupHotSet{}, errGroupHotSet
	}
	return GroupHotSet{
		PersonChannels: personChannels,
		GroupChannels:  c.total,
		TotalChannels:  personChannels + c.total,
	}, nil
}

func (c GroupCatalog) categoryIndex(index uint64) int {
	for category := len(c.counts) - 1; category >= 0; category-- {
		if index >= c.starts[category] {
			return category
		}
	}
	return 0
}

func (c GroupCatalog) categoryRange(category GroupCategory) (uint64, int, bool) {
	categoryIndex := int(category) - 1
	if categoryIndex < 0 || categoryIndex >= len(c.counts) || c.counts[categoryIndex] == 0 {
		return 0, 0, false
	}
	return c.starts[categoryIndex], c.counts[categoryIndex], true
}

func (g Group) memberOrdinalsInRange(minimum, maximum uint64) (uint64, uint64, bool) {
	if g.identity == nil || g.memberStride == 0 || g.MemberCount <= 1 || maximum < g.memberBase {
		return 0, 0, false
	}
	first := uint64(1)
	if minimum > g.memberBase {
		delta := minimum - g.memberBase
		first = delta / g.memberStride
		if delta%g.memberStride != 0 {
			first++
		}
		if first < 1 {
			first = 1
		}
	}
	last := (maximum - g.memberBase) / g.memberStride
	memberLast := uint64(g.MemberCount - 1)
	if last > memberLast {
		last = memberLast
	}
	return first, last, first <= last
}

func (c GroupCatalog) memberCount(index uint64, categoryIndex int) (int, error) {
	var minimum int
	var span uint64
	switch GroupCategory(categoryIndex + 1) {
	case GroupSmall:
		minimum, span = 5, 16
	case GroupMedium:
		minimum, span = 100, 401
	case GroupLarge:
		minimum, span = 1_000, 9_001
	case GroupVeryLarge:
		return c.veryLargeCount, nil
	default:
		return 0, errGroupIndex
	}
	draw, err := c.identity.decisionBelow("fixed-group-member-count/v1", span, index, uint64(categoryIndex))
	if err != nil {
		return 0, err
	}
	return minimum + int(draw), nil
}

func checkedGroupMemberIndex(base, memberOrdinal uint64) (uint64, error) {
	if memberOrdinal > math.MaxUint64-base {
		return 0, errGroupMemberOverflow
	}
	return base + memberOrdinal, nil
}
