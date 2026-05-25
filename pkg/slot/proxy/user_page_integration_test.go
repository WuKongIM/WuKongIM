//go:build integration
// +build integration

package proxy

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestStoreScanUsersSlotPageReadsAuthoritativeLocalSlot(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	ctx := context.Background()
	slotID := multiraft.SlotID(1)
	uidA := findUIDForSlot(t, nodes[0].cluster, uint64(slotID), "u-local-a")
	hashSlotA := mustHashSlotForKey(t, nodes[0].cluster, uidA)
	uidB := findUIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, uint64(slotID), hashSlotA, "u-local-b")
	hashSlotB := mustHashSlotForKey(t, nodes[0].cluster, uidB)
	require.NoError(t, nodes[0].db.ForHashSlot(hashSlotA).CreateUser(ctx, metadb.User{UID: uidA}))
	require.NoError(t, nodes[0].db.ForHashSlot(hashSlotB).CreateUser(ctx, metadb.User{UID: uidB}))

	page, cursor, done, err := nodes[0].store.ScanUsersSlotPage(ctx, slotID, metadb.UserCursor{}, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.False(t, done)

	page2, _, done, err := nodes[0].store.ScanUsersSlotPage(ctx, slotID, cursor, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Contains(t, userUIDsFromMeta(append(page, page2...)), uidA)
	require.Contains(t, userUIDsFromMeta(append(page, page2...)), uidB)
}

func TestStoreScanUsersSlotPageReadsAuthoritativeRemoteSlot(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	ctx := context.Background()
	slotID := multiraft.SlotID(2)
	uid := findUIDForSlot(t, nodes[1].cluster, uint64(slotID), "u-remote-scan")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, uid)
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateUser(ctx, metadb.User{UID: uid}))

	page, _, done, err := nodes[0].store.ScanUsersSlotPage(ctx, slotID, metadb.UserCursor{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Contains(t, userUIDsFromMeta(page), uid)
}

func userUIDsFromMeta(users []metadb.User) []string {
	uids := make([]string, 0, len(users))
	for _, user := range users {
		uids = append(uids, user.UID)
	}
	return uids
}
