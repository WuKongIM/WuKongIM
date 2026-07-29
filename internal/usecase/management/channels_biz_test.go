package management

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestBusinessChannelDetailAndExactMemberLookupUseAuthoritativeOperator(t *testing.T) {
	cluster := fakeNodeSnapshotReader{snapshot: control.Snapshot{
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}}
	operator := &fakeBusinessChannelOperator{
		channel:        metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, SubscriberMutationVersion: 7},
		hasSubscribers: true,
		containsUID:    "u1",
	}
	app := New(Options{Cluster: cluster, ChannelBusinessOperator: operator})

	detail, err := app.GetBusinessChannel(context.Background(), "g1", 2)
	if err != nil {
		t.Fatalf("GetBusinessChannel(): %v", err)
	}
	if !detail.Ban || !detail.HasSubscribers || detail.HasAllowlist || detail.HasDenylist {
		t.Fatalf("detail = %#v", detail)
	}

	page, err := app.ListBusinessChannelMembers(context.Background(), ListBusinessChannelMembersRequest{
		ChannelID: "g1", ChannelType: 2, ListKind: "subscribers", Limit: 100, UID: "u1",
	})
	if err != nil {
		t.Fatalf("ListBusinessChannelMembers(exact): %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].UID != "u1" || page.HasMore {
		t.Fatalf("exact page = %#v", page)
	}
}

func TestBusinessChannelCreatePatchAndCountedMutation(t *testing.T) {
	operator := &fakeBusinessChannelOperator{
		channel: metadb.Channel{
			ChannelID: "g1", ChannelType: 2, Large: 1, AllowStranger: 1,
			SubscriberCount: 20, SubscriberMutationVersion: 8,
		},
		mutationResult: metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 1},
	}
	app := New(Options{
		Cluster:                 fakeNodeSnapshotReader{snapshot: control.Snapshot{HashSlots: control.HashSlotTable{Count: 4}}},
		ChannelBusinessOperator: operator,
	})

	if _, err := app.CreateBusinessChannel(context.Background(), CreateBusinessChannelRequest{
		ChannelID: "bad#id", ChannelType: 2,
	}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("CreateBusinessChannel(invalid) error = %v", err)
	}
	if _, err := app.UpdateBusinessChannel(context.Background(), UpdateBusinessChannelRequest{
		ChannelID: "g1", ChannelType: 2, Ban: true,
	}); err != nil {
		t.Fatalf("UpdateBusinessChannel(): %v", err)
	}
	if !operator.patchFlags.Ban {
		t.Fatalf("patch flags = %#v", operator.patchFlags)
	}

	resp, err := app.MutateBusinessChannelMembers(context.Background(), MutateBusinessChannelMembersRequest{
		ChannelID: "g1", ChannelType: 2, ListKind: "allowlist", UIDs: []string{" u1 ", "u1", "u2"}, Add: true,
	})
	if err != nil {
		t.Fatalf("MutateBusinessChannelMembers(): %v", err)
	}
	if resp.RequestedCount != 2 || resp.ChangedCount != 1 {
		t.Fatalf("mutation response = %#v", resp)
	}
}

func TestBusinessChannelMemberValidationRejectsWholeBatch(t *testing.T) {
	app := New(Options{ChannelBusinessOperator: &fakeBusinessChannelOperator{
		channel: metadb.Channel{ChannelID: "g1", ChannelType: 2},
	}})
	for _, uids := range [][]string{
		{"ok", "bad uid"},
		{"ok", string([]byte{0xff})},
	} {
		_, err := app.MutateBusinessChannelMembers(context.Background(), MutateBusinessChannelMembersRequest{
			ChannelID: "g1", ChannelType: 2, ListKind: "denylist", UIDs: uids, Add: true,
		})
		if !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("MutateBusinessChannelMembers(%q) error = %v", uids, err)
		}
	}
}

func TestBusinessChannelKeyValidationKeepsLegacyStorageValidIDs(t *testing.T) {
	legacyID := " legacy#channel@" + strings.Repeat("x", 300) + " "
	got, channelType, err := validateExistingBusinessChannelKey(legacyID, 2)
	if err != nil {
		t.Fatalf("validateExistingBusinessChannelKey(): %v", err)
	}
	if got != legacyID || channelType != 2 {
		t.Fatalf("existing key = (%q, %d), want exact legacy key", got, channelType)
	}
	if _, _, err := validateNewBusinessChannelKey(legacyID, 2); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("validateNewBusinessChannelKey(legacy) error = %v", err)
	}
	if got, _, err := validateNewBusinessChannelKey(" new-channel ", 2); err != nil || got != "new-channel" {
		t.Fatalf("validateNewBusinessChannelKey(trim) = (%q, %v)", got, err)
	}
	if _, _, err := validateExistingBusinessChannelKey("__wk_internal_memberlist__/allow/2/ZzE", 2); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("validateExistingBusinessChannelKey(internal) error = %v", err)
	}
}

func TestListBusinessChannelsAggregatesAndFiltersBusinessRows(t *testing.T) {
	snapshot := control.Snapshot{
		Slots: []control.SlotAssignment{
			{SlotID: 2, DesiredPeers: []uint64{1}},
			{SlotID: 1, DesiredPeers: []uint64{1}},
		},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{
			{From: 0, To: 1, SlotID: 1},
			{From: 2, To: 3, SlotID: 2},
		}},
	}
	keyOne := businessChannelKeyForSlot(t, snapshot.HashSlots, 1, "alpha")
	keyTwo := businessChannelKeyForSlot(t, snapshot.HashSlots, 2, "alpha-remote")
	reader := newFakeBusinessChannelReader()
	reader.slotPages[1] = map[metadb.ChannelCursor]fakeBusinessChannelPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: "__wk_internal_memberlist__/allow/2/ZzE", ChannelType: 2},
				{ChannelID: keyOne, ChannelType: 2, Ban: 1, SubscriberMutationVersion: 3},
				{ChannelID: keyOne + "____cmd", ChannelType: 2},
			},
			done: true,
		},
	}
	reader.slotPages[2] = map[metadb.ChannelCursor]fakeBusinessChannelPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: keyTwo, ChannelType: 2, SendBan: 1},
				{ChannelID: "beta", ChannelType: 3},
			},
			done: true,
		},
	}
	app := New(Options{
		Cluster:               fakeNodeSnapshotReader{snapshot: snapshot},
		ChannelBusinessReader: reader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 10, TypeFilter: 2, Keyword: " alpha "})
	if err != nil {
		t.Fatalf("ListBusinessChannels() error = %v", err)
	}
	want := []BusinessChannelListItem{
		{ChannelID: keyOne, ChannelType: 2, SlotID: 1, HashSlot: routing.HashSlotForKey(keyOne, 4), Ban: true, SubscriberMutationVersion: 3},
		{ChannelID: keyTwo, ChannelType: 2, SlotID: 2, HashSlot: routing.HashSlotForKey(keyTwo, 4), SendBan: true},
	}
	if !sameBusinessChannelItems(got.Items, want) {
		t.Fatalf("items = %#v, want %#v", got.Items, want)
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false")
	}
}

func TestListBusinessChannelsReturnsCursorAndRejectsFilterMismatch(t *testing.T) {
	snapshot := control.Snapshot{
		Slots:     []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1}}},
		HashSlots: control.HashSlotTable{Count: 4, Ranges: []control.HashSlotRange{{From: 0, To: 3, SlotID: 1}}},
	}
	reader := newFakeBusinessChannelReader()
	reader.slotPages[1] = map[metadb.ChannelCursor]fakeBusinessChannelPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: "a", ChannelType: 2},
				{ChannelID: "b", ChannelType: 2},
			},
			cursor: metadb.ChannelCursor{ChannelID: "b", ChannelType: 2},
			done:   true,
		},
	}
	app := New(Options{
		Cluster:               fakeNodeSnapshotReader{snapshot: snapshot},
		ChannelBusinessReader: reader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListBusinessChannels() error = %v", err)
	}
	if !got.HasMore {
		t.Fatalf("HasMore = false, want true")
	}
	wantItem := BusinessChannelListItem{ChannelID: "a", ChannelType: 2, SlotID: 1, HashSlot: routing.HashSlotForKey("a", 4)}
	if !sameBusinessChannelItems(got.Items, []BusinessChannelListItem{wantItem}) {
		t.Fatalf("items = %#v, want %#v", got.Items, []BusinessChannelListItem{wantItem})
	}
	if got.NextCursor != (ChannelListCursor{SlotID: 1, ChannelID: "a", ChannelType: 2, KeywordHash: crc32.ChecksumIEEE(nil)}) {
		t.Fatalf("NextCursor = %#v, want cursor after a", got.NextCursor)
	}

	_, err = app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 1, Keyword: "b", Cursor: got.NextCursor})
	if err != metadb.ErrInvalidArgument {
		t.Fatalf("ListBusinessChannels() filter mismatch error = %v, want %v", err, metadb.ErrInvalidArgument)
	}
}

func TestListBusinessChannelsRoutesRemoteNodeReads(t *testing.T) {
	localReader := newFakeBusinessChannelReader()
	remoteReader := &fakeRemoteBusinessChannelReader{
		response: ListBusinessChannelsResponse{
			Items: []BusinessChannelListItem{{
				ChannelID:   "remote",
				ChannelType: 2,
				SlotID:      9,
				HashSlot:    3,
			}},
		},
	}
	app := New(Options{
		Cluster:                fakeNodeSnapshotReader{nodeID: 1},
		ChannelBusinessReader:  localReader,
		RemoteBusinessChannels: remoteReader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{NodeID: 2, Limit: 50, TypeFilter: 2, Keyword: "remote"})
	if err != nil {
		t.Fatalf("ListBusinessChannels() error = %v", err)
	}

	if len(localReader.calls) != 0 {
		t.Fatalf("local reader calls = %#v, want none for remote node", localReader.calls)
	}
	if remoteReader.req != (ListBusinessChannelsRequest{NodeID: 2, Limit: 50, TypeFilter: 2, Keyword: "remote"}) {
		t.Fatalf("remote request = %#v, want node 2 request", remoteReader.req)
	}
	if !sameBusinessChannelItems(got.Items, remoteReader.response.Items) {
		t.Fatalf("items = %#v, want remote response %#v", got.Items, remoteReader.response.Items)
	}
}

type fakeBusinessChannelReader struct {
	slotPages map[uint32]map[metadb.ChannelCursor]fakeBusinessChannelPage
	calls     []uint32
}

type fakeBusinessChannelPage struct {
	items  []metadb.Channel
	cursor metadb.ChannelCursor
	done   bool
}

func newFakeBusinessChannelReader() *fakeBusinessChannelReader {
	return &fakeBusinessChannelReader{slotPages: map[uint32]map[metadb.ChannelCursor]fakeBusinessChannelPage{}}
}

func (f *fakeBusinessChannelReader) ScanChannelsSlotPage(_ context.Context, slotID uint32, after metadb.ChannelCursor, _ int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	f.calls = append(f.calls, slotID)
	if pages := f.slotPages[slotID]; pages != nil {
		if page, ok := pages[after]; ok {
			return append([]metadb.Channel(nil), page.items...), page.cursor, page.done, nil
		}
	}
	return nil, after, true, nil
}

type fakeRemoteBusinessChannelReader struct {
	req      ListBusinessChannelsRequest
	response ListBusinessChannelsResponse
	err      error
}

type fakeBusinessChannelOperator struct {
	channel        metadb.Channel
	hasSubscribers bool
	hasAllowlist   bool
	hasDenylist    bool
	containsUID    string
	patchFlags     BusinessChannelFlags
	mutationResult metadb.SubscriberMutationResult
}

func (f *fakeBusinessChannelOperator) GetMetadata(context.Context, BusinessChannelKey) (metadb.Channel, error) {
	if f.channel.ChannelID == "" {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return f.channel, nil
}

func (f *fakeBusinessChannelOperator) CreateMetadata(_ context.Context, info BusinessChannelInfo) error {
	if f.channel.ChannelID != "" {
		return metadb.ErrAlreadyExists
	}
	f.channel = metadb.Channel{ChannelID: info.ChannelID, ChannelType: int64(info.ChannelType)}
	return nil
}

func (f *fakeBusinessChannelOperator) PatchMetadataFlags(_ context.Context, _ BusinessChannelKey, flags BusinessChannelFlags) error {
	if f.channel.ChannelID == "" {
		return metadb.ErrNotFound
	}
	f.patchFlags = flags
	return nil
}

func (f *fakeBusinessChannelOperator) HasSubscribers(context.Context, BusinessChannelKey) (bool, error) {
	return f.hasSubscribers, nil
}

func (f *fakeBusinessChannelOperator) HasAllowlist(context.Context, BusinessChannelKey) (bool, error) {
	return f.hasAllowlist, nil
}

func (f *fakeBusinessChannelOperator) HasDenylist(context.Context, BusinessChannelKey) (bool, error) {
	return f.hasDenylist, nil
}

func (f *fakeBusinessChannelOperator) ContainsSubscriber(_ context.Context, _ BusinessChannelKey, uid string) (bool, error) {
	return uid == f.containsUID, nil
}

func (f *fakeBusinessChannelOperator) ContainsAllowlistMember(_ context.Context, _ BusinessChannelKey, uid string) (bool, error) {
	return uid == f.containsUID, nil
}

func (f *fakeBusinessChannelOperator) ContainsDenylistMember(_ context.Context, _ BusinessChannelKey, uid string) (bool, error) {
	return uid == f.containsUID, nil
}

func (f *fakeBusinessChannelOperator) ListSubscribersPage(context.Context, BusinessChannelMemberPageRequest) (BusinessChannelMemberPageResult, error) {
	return BusinessChannelMemberPageResult{}, nil
}

func (f *fakeBusinessChannelOperator) ListAllowlistPage(context.Context, BusinessChannelMemberPageRequest) (BusinessChannelMemberPageResult, error) {
	return BusinessChannelMemberPageResult{}, nil
}

func (f *fakeBusinessChannelOperator) ListDenylistPage(context.Context, BusinessChannelMemberPageRequest) (BusinessChannelMemberPageResult, error) {
	return BusinessChannelMemberPageResult{}, nil
}

func (f *fakeBusinessChannelOperator) MutateSubscribersCounted(context.Context, BusinessChannelKey, []string, bool) (metadb.SubscriberMutationResult, error) {
	return f.mutationResult, nil
}

func (f *fakeBusinessChannelOperator) MutateAllowlistCounted(context.Context, BusinessChannelKey, []string, bool) (metadb.SubscriberMutationResult, error) {
	return f.mutationResult, nil
}

func (f *fakeBusinessChannelOperator) MutateDenylistCounted(context.Context, BusinessChannelKey, []string, bool) (metadb.SubscriberMutationResult, error) {
	return f.mutationResult, nil
}

func (f *fakeRemoteBusinessChannelReader) NodeBusinessChannels(_ context.Context, req ListBusinessChannelsRequest) (ListBusinessChannelsResponse, error) {
	f.req = req
	return f.response, f.err
}

func sameBusinessChannelItems(left, right []BusinessChannelListItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func businessChannelKeyForSlot(t *testing.T, table control.HashSlotTable, slotID uint32, prefix string) string {
	t.Helper()
	for i := 0; i < 10000; i++ {
		key := prefix
		if i > 0 {
			key = fmt.Sprintf("%s-%d", prefix, i)
		}
		hashSlot := routing.HashSlotForKey(key, table.Count)
		if slotIDForHashSlot(table, hashSlot) == slotID {
			return key
		}
	}
	t.Fatalf("no key found for slot %d", slotID)
	return ""
}
