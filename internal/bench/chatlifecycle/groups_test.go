package chatlifecycle

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestGroupCatalogFormalShapeAndReconstructableMembership(t *testing.T) {
	catalog := newTestGroupCatalog(t, FormalConfig())
	counts := map[GroupCategory]int{}
	memberTotal := uint64(0)
	for index := uint64(0); index < uint64(catalog.Count()); index++ {
		group, err := catalog.Group(index)
		if err != nil {
			t.Fatalf("Group(%d) error = %v", index, err)
		}
		counts[group.Category]++
		memberTotal += uint64(group.MemberCount)
		parsed, ok := catalog.IndexFromGroupID(group.ID)
		if !ok || parsed != index {
			t.Fatalf("IndexFromGroupID(%q) = %d, %v; want %d, true", group.ID, parsed, ok, index)
		}
		assertGroupMemberRange(t, group)
		var previous uint64
		for member := 0; member < group.MemberCount; member++ {
			uid, err := group.MemberUID(member)
			if err != nil {
				t.Fatalf("group %d MemberUID(%d) error = %v", index, member, err)
			}
			memberIndex, ok := catalog.identity.IndexFromUID(uid)
			if !ok {
				t.Fatalf("group %d member %d UID %q is not reconstructable", index, member, uid)
			}
			if member > 0 && memberIndex != previous+1 {
				t.Fatalf("group %d member indexes %d then %d are not unique consecutive values", index, previous, memberIndex)
			}
			previous = memberIndex
		}
	}
	if counts[GroupSmall] != 1_600 || counts[GroupMedium] != 300 || counts[GroupLarge] != 99 || counts[GroupVeryLarge] != 1 {
		t.Fatalf("category counts = %v, want 1600/300/99/1", counts)
	}
	if memberTotal == 0 {
		t.Fatal("member scan unexpectedly empty")
	}
	if _, err := catalog.Group(2_000); !errors.Is(err, errGroupIndex) {
		t.Fatalf("Group(2000) error = %v, want %v", err, errGroupIndex)
	}
}

func TestGroupPrimaryTargetsExactSharesAndCanaryIsSeparate(t *testing.T) {
	catalog := newTestGroupCatalog(t, FormalConfig())
	counts := map[GroupCategory]int{}
	for ordinal := uint64(0); ordinal < 10_000; ordinal++ {
		target, err := catalog.PrimaryTarget(ordinal)
		if err != nil {
			t.Fatalf("PrimaryTarget(%d) error = %v", ordinal, err)
		}
		counts[target.Category]++
		if target.Category == GroupVeryLarge {
			t.Fatalf("PrimaryTarget(%d) selected the separate canary group", ordinal)
		}
	}
	if counts[GroupSmall] != 8_000 || counts[GroupMedium] != 1_500 || counts[GroupLarge] != 500 {
		t.Fatalf("primary counts = %v, want 8000/1500/500", counts)
	}
	canary, err := catalog.VeryLargeCanary(7)
	if err != nil {
		t.Fatalf("VeryLargeCanary() error = %v", err)
	}
	if canary.Group.Category != GroupVeryLarge || canary.Every != time.Minute || canary.Ordinal != 7 {
		t.Fatalf("canary = %+v, want separate one/min very-large target", canary)
	}
}

func TestGroupCatalogLocalProfileHandlesMissingLargeClass(t *testing.T) {
	cfg := LocalConfig()
	catalog := newTestGroupCatalog(t, cfg)
	if catalog.Count() != 20 {
		t.Fatalf("Count() = %d, want 20", catalog.Count())
	}
	counts := map[GroupCategory]int{}
	for ordinal := uint64(0); ordinal < 95; ordinal++ {
		target, err := catalog.PrimaryTarget(ordinal)
		if err != nil {
			t.Fatalf("PrimaryTarget(%d) error = %v", ordinal, err)
		}
		counts[target.Category]++
	}
	if counts[GroupSmall] != 80 || counts[GroupMedium] != 15 || counts[GroupLarge] != 0 || counts[GroupVeryLarge] != 0 {
		t.Fatalf("normalized local primary counts = %v, want 80/15/0/0", counts)
	}
	canary, err := catalog.VeryLargeCanary(0)
	if err != nil {
		t.Fatalf("VeryLargeCanary() error = %v", err)
	}
	if canary.Group.MemberCount != 1_000 {
		t.Fatalf("local canary members = %d, want 1000", canary.Group.MemberCount)
	}
}

func TestGroupHotSetDoesNotAddHistoricalGrowth(t *testing.T) {
	catalog := newTestGroupCatalog(t, FormalConfig())
	summary, err := catalog.HotSet(8_000)
	if err != nil {
		t.Fatalf("HotSet() error = %v", err)
	}
	if summary.PersonChannels != 8_000 || summary.GroupChannels != 2_000 || summary.TotalChannels != 10_000 || summary.HistoricalGroupGrowth != 0 {
		t.Fatalf("hot set = %+v, want 8000+2000=10000 and zero group history growth", summary)
	}
}

func TestGroupCatalogRejectsInvalidOrOverflowingInputs(t *testing.T) {
	cfg := FormalConfig()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	if _, err := NewGroupCatalog(nil, cfg.Workload.Groups); !errors.Is(err, errGroupIdentityRequired) {
		t.Fatalf("NewGroupCatalog(nil) error = %v, want %v", err, errGroupIdentityRequired)
	}
	bad := cfg.Workload.Groups
	bad.Small = 2_001
	if _, err := NewGroupCatalog(identity, bad); !errors.Is(err, errGroupCatalog) {
		t.Fatalf("NewGroupCatalog(large catalog) error = %v, want %v", err, errGroupCatalog)
	}
	bad = GroupCatalogConfig{VeryLarge: 1, VeryLargeMembers: 100_000, FixedMembership: true, VeryLargeSendEvery: time.Minute}
	if _, err := NewGroupCatalog(identity, bad); !errors.Is(err, errGroupPrimaryClasses) {
		t.Fatalf("NewGroupCatalog(canary only) error = %v, want %v", err, errGroupPrimaryClasses)
	}
	bad = cfg.Workload.Groups
	bad.VeryLargeMembers = 1
	if _, err := NewGroupCatalog(identity, bad); !errors.Is(err, errGroupCatalog) {
		t.Fatalf("NewGroupCatalog(one-member canary) error = %v, want %v", err, errGroupCatalog)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog() error = %v", err)
	}
	if _, err := catalog.HotSet(0); !errors.Is(err, errGroupHotSet) {
		t.Fatalf("HotSet(0) error = %v, want %v", err, errGroupHotSet)
	}
	if _, err := checkedGroupMemberIndex(math.MaxUint64, 1); !errors.Is(err, errGroupMemberOverflow) {
		t.Fatalf("checkedGroupMemberIndex(overflow) error = %v, want %v", err, errGroupMemberOverflow)
	}
}

func assertGroupMemberRange(t *testing.T, group Group) {
	t.Helper()
	var minimum, maximum int
	switch group.Category {
	case GroupSmall:
		minimum, maximum = 5, 20
	case GroupMedium:
		minimum, maximum = 100, 500
	case GroupLarge:
		minimum, maximum = 1_000, 10_000
	case GroupVeryLarge:
		minimum, maximum = 100_000, 100_000
	default:
		t.Fatalf("unknown group category %d", group.Category)
	}
	if group.MemberCount < minimum || group.MemberCount > maximum {
		t.Fatalf("group %d category %d members = %d, want %d..%d", group.Index, group.Category, group.MemberCount, minimum, maximum)
	}
}

func newTestGroupCatalog(t *testing.T, cfg Config) GroupCatalog {
	t.Helper()
	identity, err := NewIdentitySpace(cfg.RunID, cfg.Seed, uint64(cfg.Workload.Workers))
	if err != nil {
		t.Fatalf("NewIdentitySpace() error = %v", err)
	}
	catalog, err := NewGroupCatalog(identity, cfg.Workload.Groups)
	if err != nil {
		t.Fatalf("NewGroupCatalog() error = %v", err)
	}
	return catalog
}
