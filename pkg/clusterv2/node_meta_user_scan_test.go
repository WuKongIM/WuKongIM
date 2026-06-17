package clusterv2

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterV2SingleNodeScanUsersSlotPagePaginatesMetadata(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })

	route := waitRouteKeyLeaderReady(t, node, "u1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := node.CreateUserMetadata(ctx, metadb.User{UID: "u1"}); err != nil {
		t.Fatalf("CreateUserMetadata(u1) error = %v", err)
	}
	if err := node.CreateUserMetadata(ctx, metadb.User{UID: "u2"}); err != nil {
		t.Fatalf("CreateUserMetadata(u2) error = %v", err)
	}

	page, cursor, done, err := node.ScanUsersSlotPage(ctx, route.SlotID, metadb.UserCursor{}, 1)
	if err != nil {
		t.Fatalf("ScanUsersSlotPage() error = %v", err)
	}
	if len(page) != 1 || page[0].UID != "u1" || done {
		t.Fatalf("page1 = %#v cursor=%#v done=%t, want u1 and more", page, cursor, done)
	}
	page, _, done, err = node.ScanUsersSlotPage(ctx, route.SlotID, cursor, 10)
	if err != nil {
		t.Fatalf("ScanUsersSlotPage(page2) error = %v", err)
	}
	if len(page) != 1 || page[0].UID != "u2" || !done {
		t.Fatalf("page2 = %#v done=%t, want u2 and done", page, done)
	}
}
