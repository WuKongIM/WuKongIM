package chatlifecycle

import (
	"fmt"
	"testing"
)

// Failure cases: roster members escape their fixed worker; padding is decoded
// as a member; forward/reverse membership diverges; or the half-scale cohort
// cannot supply distinct owner-local sampled-delivery participants. The fixed
// catalog, category/member counts, and identity ownership remain the inputs.
func TestGroupRosterPreservesWorkerOwnershipAndReverseMembership(t *testing.T) {
	for _, workers := range []uint64{1, 2, 3, 4, 7, 16} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			id, err := NewIdentitySpace("group-roster-ownership", 1, workers)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := NewGroupCatalog(id, FormalConfig().Workload.Groups)
			if err != nil {
				t.Fatal(err)
			}
			for index := uint64(0); index < uint64(catalog.Count()); index++ {
				group, e := catalog.Group(index)
				if e != nil {
					t.Fatal(e)
				}
				wantOwner, _ := catalog.GroupOwner(index)
				for _, ordinal := range []int{0, 1, group.MemberCount - 1} {
					member, e := group.MemberIndex(ordinal)
					if e != nil {
						t.Fatal(e)
					}
					owner, _ := id.Owner(member)
					if owner != wantOwner {
						t.Fatalf("group=%d ordinal=%d member_owner=%d want=%d", index, ordinal, owner, wantOwner)
					}
					decoded, gotOrdinal, ok, e := catalog.GroupForMemberIndex(member)
					if e != nil || !ok || decoded.Index != index || gotOrdinal != ordinal || !group.ContainsIndex(member) {
						t.Fatalf("reverse group=%d ordinal=%d -> group=%d ordinal=%d member=%v err=%v", index, ordinal, decoded.Index, gotOrdinal, ok, e)
					}
				}
			}
		})
	}
}
func TestGroupRosterPaddingIsNotAMember(t *testing.T) {
	id, err := NewIdentitySpace("group-roster-padding", 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewGroupCatalog(id, FormalConfig().Workload.Groups)
	if err != nil {
		t.Fatal(err)
	}
	// 2000 fixed groups occupy the first 2000 residues of a 2001-wide,
	// three-worker-aligned cohort. The extra residue owns no group.
	for _, index := range []uint64{2000, 4001, 6002} {
		_, _, member, e := c.GroupForMemberIndex(index)
		if e != nil || member {
			t.Fatalf("padding index=%d member=%v err=%v", index, member, e)
		}
	}
	for index := uint64(0); index < 2000; index++ {
		g, e := c.Group(index)
		if e != nil {
			t.Fatal(e)
		}
		second, e := g.MemberIndex(1)
		if e != nil {
			t.Fatal(e)
		}
		if second >= 5000 {
			t.Fatalf("half-scale group=%d second member outside online prefix: %d", index, second)
		}
		after, e := checkedGroupMemberIndex(g.memberBase, uint64(g.MemberCount)*g.memberStride)
		if e != nil {
			t.Fatal(e)
		}
		_, _, member, e := c.GroupForMemberIndex(after)
		if e != nil || member {
			t.Fatalf("past-end group=%d index=%d member=%v err=%v", index, after, member, e)
		}
	}
}
