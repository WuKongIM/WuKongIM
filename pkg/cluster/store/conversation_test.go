package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

type deleteConversationsCall struct {
	uid      string
	channels []wkdb.Channel
}

type addOrUpdateUserConversationsCall struct {
	uid           string
	conversations []wkdb.Conversation
}

type trackingDeleteDB struct {
	wkdb.DB
	deleteConversationCalls  []wkdb.Conversation
	deleteConversationsCalls []deleteConversationsCall
	addOrUpdateCalls         []addOrUpdateUserConversationsCall
	addOrUpdateBatchCalls    []wkdb.Conversation
	operationOrder           []string
}

func (t *trackingDeleteDB) DeleteConversation(uid string, channelId string, channelType uint8) error {
	t.deleteConversationCalls = append(t.deleteConversationCalls, wkdb.Conversation{
		Uid:         uid,
		ChannelId:   channelId,
		ChannelType: channelType,
	})
	t.operationOrder = append(t.operationOrder, fmt.Sprintf("delete-single:%s:%s:%d", uid, channelId, channelType))
	return nil
}

func (t *trackingDeleteDB) DeleteConversations(uid string, channels []wkdb.Channel) error {
	t.deleteConversationsCalls = append(t.deleteConversationsCalls, deleteConversationsCall{
		uid:      uid,
		channels: append([]wkdb.Channel(nil), channels...),
	})
	t.operationOrder = append(t.operationOrder, fmt.Sprintf("delete-batch:%s:%d", uid, len(channels)))
	return nil
}

func (t *trackingDeleteDB) AddOrUpdateConversationsWithUser(uid string, conversations []wkdb.Conversation) error {
	t.addOrUpdateCalls = append(t.addOrUpdateCalls, addOrUpdateUserConversationsCall{
		uid:           uid,
		conversations: append([]wkdb.Conversation(nil), conversations...),
	})
	t.operationOrder = append(t.operationOrder, fmt.Sprintf("add-update-user:%s:%d", uid, len(conversations)))
	return nil
}

func (t *trackingDeleteDB) AddOrUpdateConversationsBatchIfNotExist(conversations []wkdb.Conversation) error {
	t.addOrUpdateBatchCalls = append(t.addOrUpdateBatchCalls, append([]wkdb.Conversation(nil), conversations...)...)
	t.operationOrder = append(t.operationOrder, fmt.Sprintf("add-update-batch-if-not-exist:%d", len(conversations)))
	return nil
}

type fakeSlot struct {
	proposals [][]byte
}

func (f *fakeSlot) SlotLeaderId(slotId uint32) (nodeId uint64) {
	return 0
}

func (f *fakeSlot) GetSlotId(v string) uint32 {
	return 1
}

func (f *fakeSlot) Propose(slotId uint32, data []byte) (*types.ProposeResp, error) {
	return nil, nil
}

func (f *fakeSlot) ProposeUntilApplied(slotId uint32, data []byte) (*types.ProposeResp, error) {
	cp := append([]byte(nil), data...)
	f.proposals = append(f.proposals, cp)
	return nil, nil
}

func (f *fakeSlot) ProposeUntilAppliedTimeout(ctx context.Context, slotId uint32, data []byte) (*types.ProposeResp, error) {
	return f.ProposeUntilApplied(slotId, data)
}

func TestDeleteConversationsSplitsOversizedBatches(t *testing.T) {
	slot := &fakeSlot{}
	s := New(NewOptions(WithSlot(slot)))

	channels := make([]wkdb.Channel, 1001)
	for i := range channels {
		channels[i] = wkdb.Channel{
			ChannelId:   fmt.Sprintf("channel-%d", i),
			ChannelType: 1,
		}
	}

	if err := s.DeleteConversations("uid-1", channels); err != nil {
		t.Fatalf("DeleteConversations returned error: %v", err)
	}

	if got := len(slot.proposals); got != 2 {
		t.Fatalf("expected 2 proposals, got %d", got)
	}

	for i, proposal := range slot.proposals {
		var cmd CMD
		if err := cmd.Unmarshal(proposal); err != nil {
			t.Fatalf("proposal %d: unmarshal cmd: %v", i, err)
		}
		if cmd.CmdType != CMDDeleteConversations {
			t.Fatalf("proposal %d: expected cmd type %v, got %v", i, CMDDeleteConversations, cmd.CmdType)
		}

		uid, chunk, err := cmd.DecodeCMDDeleteConversations()
		if err != nil {
			t.Fatalf("proposal %d: decode delete conversations: %v", i, err)
		}
		if uid != "uid-1" {
			t.Fatalf("proposal %d: expected uid %q, got %q", i, "uid-1", uid)
		}

		expectedLen := 1000
		if i == 1 {
			expectedLen = 1
		}
		if got := len(chunk); got != expectedLen {
			t.Fatalf("proposal %d: expected %d channels, got %d", i, expectedLen, got)
		}
	}
}

func TestDeleteConversationsNoopForEmptyInput(t *testing.T) {
	slot := &fakeSlot{}
	s := New(NewOptions(WithSlot(slot)))

	if err := s.DeleteConversations("uid-1", nil); err != nil {
		t.Fatalf("DeleteConversations returned error: %v", err)
	}

	if got := len(slot.proposals); got != 0 {
		t.Fatalf("expected 0 proposals, got %d", got)
	}
}

func TestDeleteConversationsKeepsExactBoundaryInOneProposal(t *testing.T) {
	slot := &fakeSlot{}
	s := New(NewOptions(WithSlot(slot)))

	channels := make([]wkdb.Channel, 1000)
	for i := range channels {
		channels[i] = wkdb.Channel{
			ChannelId:   fmt.Sprintf("channel-%d", i),
			ChannelType: 1,
		}
	}

	if err := s.DeleteConversations("uid-1", channels); err != nil {
		t.Fatalf("DeleteConversations returned error: %v", err)
	}

	if got := len(slot.proposals); got != 1 {
		t.Fatalf("expected 1 proposal, got %d", got)
	}

	var cmd CMD
	if err := cmd.Unmarshal(slot.proposals[0]); err != nil {
		t.Fatalf("unmarshal cmd: %v", err)
	}
	uid, chunk, err := cmd.DecodeCMDDeleteConversations()
	if err != nil {
		t.Fatalf("decode delete conversations: %v", err)
	}
	if uid != "uid-1" {
		t.Fatalf("expected uid %q, got %q", "uid-1", uid)
	}
	if got := len(chunk); got != 1000 {
		t.Fatalf("expected 1000 channels, got %d", got)
	}
}

func TestApplyCMDsAggregatesConversationDeletesByUID(t *testing.T) {
	db := &trackingDeleteDB{}
	s := New(NewOptions(WithDB(db)))

	cmds := []*CMD{
		NewCMD(CMDDeleteConversation, EncodeCMDDeleteConversation("uid-b", "channel-b-1", 1)),
		NewCMD(CMDDeleteConversations, EncodeCMDDeleteConversations("uid-a", []wkdb.Channel{{ChannelId: "channel-a-1", ChannelType: 1}, {ChannelId: "channel-a-2", ChannelType: 2}})),
		NewCMD(CMDDeleteConversation, EncodeCMDDeleteConversation("uid-a", "channel-a-3", 3)),
		NewCMD(CMDDeleteConversations, EncodeCMDDeleteConversations("uid-b", []wkdb.Channel{{ChannelId: "channel-b-2", ChannelType: 4}})),
		NewCMD(CMDDeleteConversation, EncodeCMDDeleteConversation("uid-a", "channel-a-4", 5)),
	}

	if err := s.applyCMDs(cmds, []uint64{1, 2, 3, 4, 5}); err != nil {
		t.Fatalf("applyCMDs returned error: %v", err)
	}

	if got := len(db.deleteConversationCalls); got != 0 {
		t.Fatalf("expected applyCMDs to avoid single DeleteConversation calls, got %d", got)
	}

	if got := len(db.deleteConversationsCalls); got != 2 {
		t.Fatalf("expected 2 batched DeleteConversations calls, got %d", got)
	}

	first := db.deleteConversationsCalls[0]
	if first.uid != "uid-b" {
		t.Fatalf("expected first batch uid %q, got %q", "uid-b", first.uid)
	}
	assertChannelsEqual(t, first.channels, []wkdb.Channel{{ChannelId: "channel-b-1", ChannelType: 1}, {ChannelId: "channel-b-2", ChannelType: 4}})

	second := db.deleteConversationsCalls[1]
	if second.uid != "uid-a" {
		t.Fatalf("expected second batch uid %q, got %q", "uid-a", second.uid)
	}
	assertChannelsEqual(t, second.channels, []wkdb.Channel{{ChannelId: "channel-a-1", ChannelType: 1}, {ChannelId: "channel-a-2", ChannelType: 2}, {ChannelId: "channel-a-3", ChannelType: 3}, {ChannelId: "channel-a-4", ChannelType: 5}})
}

func TestApplyCMDRetainsSingleDeleteRouting(t *testing.T) {
	db := &trackingDeleteDB{}
	s := New(NewOptions(WithDB(db)))

	if err := s.applyCMD(NewCMD(CMDDeleteConversation, EncodeCMDDeleteConversation("uid-1", "channel-1", 1)), 1); err != nil {
		t.Fatalf("applyCMD delete conversation returned error: %v", err)
	}
	if err := s.applyCMD(NewCMD(CMDDeleteConversations, EncodeCMDDeleteConversations("uid-2", []wkdb.Channel{{ChannelId: "channel-2", ChannelType: 2}})), 2); err != nil {
		t.Fatalf("applyCMD delete conversations returned error: %v", err)
	}

	if got := len(db.deleteConversationCalls); got != 1 {
		t.Fatalf("expected 1 single DeleteConversation call, got %d", got)
	}
	single := db.deleteConversationCalls[0]
	if single.Uid != "uid-1" || single.ChannelId != "channel-1" || single.ChannelType != 1 {
		t.Fatalf("unexpected single delete call: %+v", single)
	}

	if got := len(db.deleteConversationsCalls); got != 1 {
		t.Fatalf("expected 1 DeleteConversations call, got %d", got)
	}
	batch := db.deleteConversationsCalls[0]
	if batch.uid != "uid-2" {
		t.Fatalf("expected batch uid %q, got %q", "uid-2", batch.uid)
	}
	assertChannelsEqual(t, batch.channels, []wkdb.Channel{{ChannelId: "channel-2", ChannelType: 2}})
}

func TestApplyCMDsFlushesDeleteRunsBeforeGroupedHandlers(t *testing.T) {
	db := &trackingDeleteDB{}
	s := New(NewOptions(WithDB(db)))

	firstUserConversations := []wkdb.Conversation{{Uid: "uid-grouped", ChannelId: "grouped-1", ChannelType: 1}}
	firstUserData, err := EncodeCMDAddOrUpdateUserConversations("uid-grouped", firstUserConversations)
	if err != nil {
		t.Fatalf("encode first grouped conversations: %v", err)
	}

	batchIfNotExistConversations := []wkdb.Conversation{{Uid: "uid-grouped-2", ChannelId: "grouped-2", ChannelType: 2}}
	batchIfNotExistData, err := EncodeCMDAddOrUpdateConversations(batchIfNotExistConversations)
	if err != nil {
		t.Fatalf("encode batch if not exist conversations: %v", err)
	}

	cmds := []*CMD{
		NewCMD(CMDDeleteConversation, EncodeCMDDeleteConversation("uid-delete-1", "channel-1", 1)),
		NewCMD(CMDDeleteConversations, EncodeCMDDeleteConversations("uid-delete-1", []wkdb.Channel{{ChannelId: "channel-2", ChannelType: 2}})),
		NewCMD(CMDAddOrUpdateUserConversations, firstUserData),
		NewCMD(CMDDeleteConversation, EncodeCMDDeleteConversation("uid-delete-2", "channel-3", 3)),
		NewCMD(CMDAddOrUpdateConversationsBatchIfNotExist, batchIfNotExistData),
	}

	if err := s.applyCMDs(cmds, []uint64{1, 2, 3, 4, 5}); err != nil {
		t.Fatalf("applyCMDs returned error: %v", err)
	}

	expectedOrder := []string{
		"delete-batch:uid-delete-1:2",
		"add-update-user:uid-grouped:1",
		"delete-batch:uid-delete-2:1",
		"add-update-batch-if-not-exist:1",
	}
	assertOperationOrder(t, db.operationOrder, expectedOrder)
}

func assertChannelsEqual(t *testing.T, actual []wkdb.Channel, expected []wkdb.Channel) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("expected %d channels, got %d", len(expected), len(actual))
	}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Fatalf("channel %d: expected %+v, got %+v", i, expected[i], actual[i])
		}
	}
}

func assertOperationOrder(t *testing.T, actual []string, expected []string) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("expected %d operations, got %d: %v", len(expected), len(actual), actual)
	}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Fatalf("operation %d: expected %q, got %q (all=%v)", i, expected[i], actual[i], actual)
		}
	}
}
