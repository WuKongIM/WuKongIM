package presence

import (
	"testing"
	"time"
)

func TestDirectoryExpiryIndexTracksTimestampedRoutes(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 7, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)

	for _, route := range []Route{
		{UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, LastSeenUnix: 100},
		{UID: "u2", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 20, LastSeenUnix: 100},
		{UID: "untimestamped", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 30},
	} {
		if _, err := dir.RegisterRoute(target, route); err != nil {
			t.Fatalf("RegisterRoute(%q) error = %v", route.UID, err)
		}
	}

	snap := dir.Snapshot()
	if snap.Active != 3 || snap.ExpiryIndexRoutes != 2 || snap.ExpiryIndexBuckets != 1 {
		t.Fatalf("snapshot = %#v, want active=3 index routes=2 buckets=1", snap)
	}

	result := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second)
	if result.Expired != 2 || result.Examined != 2 || result.DueBuckets != 1 {
		t.Fatalf("expire result = %#v, want expired=2 examined=2 due buckets=1", result)
	}
	if result.IndexRoutes != 0 || result.IndexBuckets != 0 {
		t.Fatalf("expire result = %#v, want empty expiry index", result)
	}
	if snap := dir.Snapshot(); snap.Active != 1 || snap.ExpiryIndexRoutes != 0 || snap.ExpiryIndexBuckets != 0 {
		t.Fatalf("post-expiry snapshot = %#v, want only untimestamped route", snap)
	}
}

func TestDirectoryExpiryRetainsExactDeadlineEquality(t *testing.T) {
	dir := NewDirectory(DirectoryOptions{ShardCount: 1})
	target := RouteTarget{HashSlot: 7, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1}
	dir.BecomeAuthority(target)
	if _, err := dir.RegisterRoute(target, Route{
		UID: "u1", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 10, LastSeenUnix: 100,
	}); err != nil {
		t.Fatalf("RegisterRoute() error = %v", err)
	}

	atDeadline := dir.ExpireRoutesDetailed(time.Unix(105, 0), 5*time.Second)
	if atDeadline.Expired != 0 || atDeadline.Examined != 0 || atDeadline.DueBuckets != 0 {
		t.Fatalf("expire at deadline = %#v, want no due candidates", atDeadline)
	}
	if atDeadline.IndexRoutes != 1 || atDeadline.IndexBuckets != 1 {
		t.Fatalf("expire at deadline = %#v, want route retained in expiry index", atDeadline)
	}

	afterDeadline := dir.ExpireRoutesDetailed(time.Unix(106, 0), 5*time.Second)
	if afterDeadline.Expired != 1 || afterDeadline.Examined != 1 || afterDeadline.DueBuckets != 1 {
		t.Fatalf("expire after deadline = %#v, want route expired", afterDeadline)
	}
}
