package fsm

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestPersonDirectoryBatchCommandsCanonicalizeAndExposeOwnedHashSlots(t *testing.T) {
	memberships := []UserChannelMembershipBatchItem{
		{HashSlot: 7, Membership: metadb.UserChannelMembership{UID: "u2", ChannelID: "u1@u2", ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 2}},
		{HashSlot: 2, Membership: metadb.UserChannelMembership{UID: "u1", ChannelID: "u1@u2", ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 2}},
	}
	encoded, err := EncodeUpsertUserChannelMembershipBatchCommandChecked(memberships)
	if err != nil {
		t.Fatalf("EncodeUpsertUserChannelMembershipBatchCommandChecked() error = %v", err)
	}
	reordered, err := EncodeUpsertUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{memberships[1], memberships[0]})
	if err != nil {
		t.Fatalf("EncodeUpsertUserChannelMembershipBatchCommandChecked(reordered) error = %v", err)
	}
	if !bytes.Equal(encoded, reordered) {
		t.Fatal("membership batch encoding depends on caller order")
	}
	decoded, err := decodeCommand(encoded)
	if err != nil {
		t.Fatalf("decodeCommand(memberships) error = %v", err)
	}
	if got := decoded.(scopedHashSlotCommand).applyHashSlots(0); !reflect.DeepEqual(got, []uint16{2, 7}) {
		t.Fatalf("membership apply hash slots = %#v, want [2 7]", got)
	}

	ready := []ChannelDirectoryReadyBatchItem{
		{HashSlot: 9, ChannelID: "u3@u4", ChannelType: 1},
		{HashSlot: 3, ChannelID: "u1@u2", ChannelType: 1},
	}
	encoded, err = EncodeEnsureChannelDirectoriesReadyBatchCommandChecked(ready)
	if err != nil {
		t.Fatalf("EncodeEnsureChannelDirectoriesReadyBatchCommandChecked() error = %v", err)
	}
	reordered, err = EncodeEnsureChannelDirectoriesReadyBatchCommandChecked([]ChannelDirectoryReadyBatchItem{ready[1], ready[0]})
	if err != nil {
		t.Fatalf("EncodeEnsureChannelDirectoriesReadyBatchCommandChecked(reordered) error = %v", err)
	}
	if !bytes.Equal(encoded, reordered) {
		t.Fatal("directory-ready batch encoding depends on caller order")
	}
	decoded, err = decodeCommand(encoded)
	if err != nil {
		t.Fatalf("decodeCommand(ready) error = %v", err)
	}
	if got := decoded.(scopedHashSlotCommand).applyHashSlots(0); !reflect.DeepEqual(got, []uint16{3, 9}) {
		t.Fatalf("ready apply hash slots = %#v, want [3 9]", got)
	}
}

func TestPreparePersonChannelDirectoryBatchCombinesMembershipAndRuntimeMeta(t *testing.T) {
	memberships := []UserChannelMembershipBatchItem{{
		HashSlot: 2,
		Membership: metadb.UserChannelMembership{
			UID: "u1", ChannelID: "u1@u2", ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 2,
		},
	}}
	metas := []CreateChannelRuntimeMetaBatchItem{{
		HashSlot: 7,
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID: "u1@u2", ChannelType: 1, Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1,
		},
	}}
	encoded, err := EncodePreparePersonChannelDirectoryBatchCommandChecked(memberships, metas)
	if err != nil {
		t.Fatalf("EncodePreparePersonChannelDirectoryBatchCommandChecked() error = %v", err)
	}
	if !IsCreateChannelRuntimeMetaCommand(encoded) {
		t.Fatal("combined directory prepare command did not expose runtime-meta creation")
	}
	if count, err := CreateChannelRuntimeMetaBatchCommandSize(encoded); err != nil || count != 1 {
		t.Fatalf("runtime-meta create count = %d, err=%v, want 1", count, err)
	}
	if slots, err := DecodeCommandHashSlots(encoded, 2); err != nil || !reflect.DeepEqual(slots, []uint16{2, 7}) {
		t.Fatalf("combined command hash slots = %#v, err=%v, want [2 7]", slots, err)
	}
}

func TestPreparePersonChannelDirectoryBatchRejectsNonCanonicalWireOrder(t *testing.T) {
	memberships := []UserChannelMembershipBatchItem{{
		HashSlot: 2,
		Membership: metadb.UserChannelMembership{
			UID: "u1", ChannelID: "u1@u2", ChannelType: 1, JoinSeq: 1, SourceVersion: 1, UpdatedAt: 2,
		},
	}}
	metas := []CreateChannelRuntimeMetaBatchItem{{
		HashSlot: 7,
		Meta: metadb.ChannelRuntimeMeta{
			ChannelID: "u1@u2", ChannelType: 1, Replicas: []uint64{1}, ISR: []uint64{1}, Leader: 1, MinISR: 1,
		},
	}}
	encoded, err := EncodePreparePersonChannelDirectoryBatchCommandChecked(memberships, metas)
	if err != nil {
		t.Fatalf("EncodePreparePersonChannelDirectoryBatchCommandChecked() error = %v", err)
	}
	_, _, membershipBytes, err := readTLV(encoded[headerSize:])
	if err != nil {
		t.Fatalf("read membership TLV: %v", err)
	}
	nonCanonical := append([]byte(nil), encoded[:headerSize]...)
	nonCanonical = append(nonCanonical, encoded[headerSize+membershipBytes:]...)
	nonCanonical = append(nonCanonical, encoded[headerSize:headerSize+membershipBytes]...)
	if _, err := decodeCommand(nonCanonical); !errors.Is(err, metadb.ErrCorruptValue) {
		t.Fatalf("decode non-canonical prepare command error = %v, want corrupt value", err)
	}
}

func TestPersonDirectoryBatchCommandsRejectDuplicateAndUnboundedInput(t *testing.T) {
	membership := UserChannelMembershipBatchItem{HashSlot: 1, Membership: metadb.UserChannelMembership{UID: "u1", ChannelID: "u1@u2", ChannelType: 1}}
	if _, err := EncodeUpsertUserChannelMembershipBatchCommandChecked([]UserChannelMembershipBatchItem{membership, membership}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("duplicate membership error = %v, want invalid argument", err)
	}
	ready := ChannelDirectoryReadyBatchItem{HashSlot: 1, ChannelID: "u1@u2", ChannelType: 1}
	if _, err := EncodeEnsureChannelDirectoriesReadyBatchCommandChecked([]ChannelDirectoryReadyBatchItem{ready, ready}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("duplicate ready error = %v, want invalid argument", err)
	}
	tooMany := make([]ChannelDirectoryReadyBatchItem, MaxPersonDirectoryBatchItems+1)
	for i := range tooMany {
		tooMany[i] = ChannelDirectoryReadyBatchItem{HashSlot: uint16(i), ChannelID: string(rune('a' + i)), ChannelType: 1}
	}
	if _, err := EncodeEnsureChannelDirectoriesReadyBatchCommandChecked(tooMany); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unbounded ready error = %v, want invalid argument", err)
	}
}
