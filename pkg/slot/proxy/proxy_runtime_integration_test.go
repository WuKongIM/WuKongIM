//go:build integration

package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func proposalPayload(data []byte) []byte {
	payload := make([]byte, 10+len(data))
	binary.BigEndian.PutUint16(payload[:2], 0)
	copy(payload[10:], data)
	return payload
}

func TestMemoryBackedGroupAppliesProposalToWKDB(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(51)
	db := openTestDB(t)
	rt := newStartedRuntime(t)

	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftstorage.NewMemory(),
			StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:         "u1",
		Token:       "t1",
		DeviceFlag:  1,
		DeviceLevel: 2,
	})))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	got, err := db.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "t1" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestMemoryBackedGroupDoesNotRecoverDeletedSlotDataAfterOpenGroup(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(51)
	db := openTestDB(t)

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftstorage.NewMemory(),
			StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "t1",
	})))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := db.DeleteSlotData(ctx, uint64(slotID)); err != nil {
		t.Fatalf("DeleteSlotData() error = %v", err)
	}
	if applied, err := db.SlotAppliedIndex(ctx, uint64(slotID)); err != nil || applied != 0 {
		t.Fatalf("SlotAppliedIndex() after delete = %d, %v; want 0, nil", applied, err)
	}
	if _, err := db.ForSlot(uint64(slotID)).GetUser(ctx, "u1"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after delete err = %v, want ErrNotFound", err)
	}

	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      raftstorage.NewMemory(),
		StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	_, err = db.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after reopen err = %v, want ErrNotFound", err)
	}
}

func TestMemoryBackedGroupReopensWithRecoveredMembership(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(52)
	db := openTestDB(t)
	store := raftstorage.NewMemory()

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "before-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(before reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(before reopen) error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      store,
		StateMachine: mustNewStateMachine(t, db, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := reopenRT.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "reopened slot become leader")

	fut, err = reopenRT.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "after-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(after reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(after reopen) error = %v", err)
	}

	got, err := db.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "after-reopen" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestPebbleBackedGroupReopensAndAcceptsNewProposal(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(61)
	root := t.TempDir()
	bizPath := filepath.Join(root, "biz")
	raftPath := filepath.Join(root, "raft")

	bizDB := openTestDBAt(t, bizPath)
	raftDB := openTestRaftDBAt(t, raftPath)

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftDB.ForSlot(uint64(slotID)),
			StateMachine: mustNewStateMachine(t, bizDB, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "before-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(before reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(before reopen) error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := bizDB.Close(); err != nil {
		t.Fatalf("bizDB.Close() error = %v", err)
	}
	if err := raftDB.Close(); err != nil {
		t.Fatalf("raftDB.Close() error = %v", err)
	}

	reopenedBizDB := openTestDBAt(t, bizPath)
	reopenedRaftDB := openTestRaftDBAt(t, raftPath)
	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      reopenedRaftDB.ForSlot(uint64(slotID)),
		StateMachine: mustNewStateMachine(t, reopenedBizDB, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := reopenRT.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "reopened slot become leader")

	fut, err = reopenRT.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "after-reopen",
	})))
	if err != nil {
		t.Fatalf("Propose(after reopen) error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait(after reopen) error = %v", err)
	}

	got, err := reopenedBizDB.ForSlot(uint64(slotID)).GetUser(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got.Token != "after-reopen" {
		t.Fatalf("stored user = %#v", got)
	}
}

func TestPebbleBackedGroupDoesNotRecoverDeletedBusinessStateWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	slotID := multiraft.SlotID(62)
	root := t.TempDir()
	bizPath := filepath.Join(root, "biz")
	raftPath := filepath.Join(root, "raft")

	bizDB := openTestDBAt(t, bizPath)
	raftDB := openTestRaftDBAt(t, raftPath)

	rt := newStartedRuntime(t)
	if err := rt.BootstrapSlot(ctx, multiraft.BootstrapSlotRequest{
		Slot: multiraft.SlotOptions{
			ID:           slotID,
			Storage:      raftDB.ForSlot(uint64(slotID)),
			StateMachine: mustNewStateMachine(t, bizDB, uint64(slotID)),
		},
		Voters: []multiraft.NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == multiraft.RoleLeader
	}, "slot become leader")

	fut, err := rt.Propose(ctx, slotID, proposalPayload(metafsm.EncodeUpsertUserCommand(metadb.User{
		UID:   "u1",
		Token: "t1",
	})))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := bizDB.Close(); err != nil {
		t.Fatalf("bizDB.Close() error = %v", err)
	}
	if err := raftDB.Close(); err != nil {
		t.Fatalf("raftDB.Close() error = %v", err)
	}

	reopenedBizDB := openTestDBAt(t, bizPath)
	reopenedRaftDB := openTestRaftDBAt(t, raftPath)

	if err := reopenedBizDB.DeleteSlotData(ctx, uint64(slotID)); err != nil {
		t.Fatalf("DeleteSlotData() error = %v", err)
	}
	if _, err := reopenedBizDB.ForSlot(uint64(slotID)).GetUser(ctx, "u1"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after delete err = %v, want ErrNotFound", err)
	}

	reopenRT := newStartedRuntime(t)
	if err := reopenRT.OpenSlot(ctx, multiraft.SlotOptions{
		ID:           slotID,
		Storage:      reopenedRaftDB.ForSlot(uint64(slotID)),
		StateMachine: mustNewStateMachine(t, reopenedBizDB, uint64(slotID)),
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	if _, err := reopenedBizDB.ForSlot(uint64(slotID)).GetUser(ctx, "u1"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("GetUser() after reopen err = %v, want ErrNotFound", err)
	}
}
