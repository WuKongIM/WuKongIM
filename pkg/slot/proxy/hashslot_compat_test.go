package proxy

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

type fakeHashSlotKeyer struct {
	hashSlot uint16
}

func (f fakeHashSlotKeyer) HashSlotForKey(string) uint16 {
	return f.hashSlot
}

type fakeHashSlotProposer struct {
	called   bool
	slotID   multiraft.SlotID
	hashSlot uint16
	cmd      []byte
}

func (f *fakeHashSlotProposer) ProposeWithHashSlot(_ context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	f.called = true
	f.slotID = slotID
	f.hashSlot = hashSlot
	f.cmd = append([]byte(nil), cmd...)
	return nil
}

type fakeLocalHashSlotProposer struct {
	called   bool
	slotID   multiraft.SlotID
	hashSlot uint16
	cmd      []byte
}

func (f *fakeLocalHashSlotProposer) ProposeLocalWithHashSlot(_ context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) error {
	f.called = true
	f.slotID = slotID
	f.hashSlot = hashSlot
	f.cmd = append([]byte(nil), cmd...)
	return nil
}

type fakeProposer struct {
	called bool
	slotID multiraft.SlotID
	cmd    []byte
}

func (f *fakeProposer) Propose(_ context.Context, slotID multiraft.SlotID, cmd []byte) error {
	f.called = true
	f.slotID = slotID
	f.cmd = append([]byte(nil), cmd...)
	return nil
}

func TestHashSlotForKeyUsesOptionalInterface(t *testing.T) {
	require.Equal(t, uint16(7), hashSlotForKey(fakeHashSlotKeyer{hashSlot: 7}, "ignored"))
}

func TestHashSlotForKeyWithoutHashSlotAPIReturnsZero(t *testing.T) {
	require.Zero(t, hashSlotForKey(struct{}{}, "key"))
}

func TestProposeWithHashSlotUsesOptionalInterface(t *testing.T) {
	proposer := &fakeHashSlotProposer{}
	err := proposeWithHashSlot(context.Background(), proposer, 9, 3, []byte("cmd"))
	require.NoError(t, err)
	require.True(t, proposer.called)
	require.Equal(t, multiraft.SlotID(9), proposer.slotID)
	require.Equal(t, uint16(3), proposer.hashSlot)
	require.Equal(t, []byte("cmd"), proposer.cmd)
}

func TestProposeWithHashSlotFallsBackToPropose(t *testing.T) {
	proposer := &fakeProposer{}
	err := proposeWithHashSlot(context.Background(), proposer, 9, 3, []byte("cmd"))
	require.NoError(t, err)
	require.True(t, proposer.called)
	require.Equal(t, multiraft.SlotID(9), proposer.slotID)
	require.Equal(t, []byte("cmd"), proposer.cmd)
}

func TestProposeLocalWithHashSlotUsesOptionalInterface(t *testing.T) {
	proposer := &fakeLocalHashSlotProposer{}
	err := proposeLocalWithHashSlot(context.Background(), proposer, 9, 3, []byte("cmd"))
	require.NoError(t, err)
	require.True(t, proposer.called)
	require.Equal(t, multiraft.SlotID(9), proposer.slotID)
	require.Equal(t, uint16(3), proposer.hashSlot)
	require.Equal(t, []byte("cmd"), proposer.cmd)
}

func TestProposeLocalWithHashSlotWithoutSupportReturnsError(t *testing.T) {
	err := proposeLocalWithHashSlot(context.Background(), struct{}{}, 9, 3, []byte("cmd"))
	require.Error(t, err)
}
