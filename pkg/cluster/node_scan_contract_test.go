package cluster

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"sort"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodePhysicalSlotScansMergeHashSlotsDeterministically(t *testing.T) {
	node, db := newLocalMetadataScanNode(t)
	ctx := context.Background()
	ids := append(
		distinctChannelIDsForHashSlot(t, 4, 0, 2),
		distinctChannelIDsForHashSlot(t, 4, 1, 2)...,
	)

	for index, id := range ids {
		hashSlot := routing.HashSlotForKey(id, 4)
		if err := db.ForHashSlot(hashSlot).CreateUser(ctx, metadb.User{UID: id, Token: "token"}); err != nil {
			t.Fatalf("CreateUser(%q) error = %v", id, err)
		}
		channel := metadb.Channel{ChannelID: id, ChannelType: 2, Ban: int64(index % 2)}
		if err := db.ForHashSlot(hashSlot).UpsertChannel(ctx, channel); err != nil {
			t.Fatalf("UpsertChannel(%q) error = %v", id, err)
		}
		meta := metadb.ChannelRuntimeMeta{
			ChannelID: id, ChannelType: 2, ChannelEpoch: uint64(index + 1),
			LeaderEpoch: 1, Leader: 1, Replicas: []uint64{1}, ISR: []uint64{1},
			MinISR: 1, Status: uint8(channelruntime.StatusActive),
		}
		if err := db.ForHashSlot(hashSlot).UpsertChannelRuntimeMeta(ctx, meta); err != nil {
			t.Fatalf("UpsertChannelRuntimeMeta(%q) error = %v", id, err)
		}
	}

	wantUsers := append([]string(nil), ids...)
	sort.Strings(wantUsers)
	firstUsers, userCursor, done, err := node.ScanUsersSlotPage(ctx, 1, metadb.UserCursor{}, 3)
	if err != nil {
		t.Fatalf("ScanUsersSlotPage(first) error = %v", err)
	}
	if done || !reflect.DeepEqual(userIDs(firstUsers), wantUsers[:3]) || userCursor.UID != wantUsers[2] {
		t.Fatalf("first user page=%#v cursor=%#v done=%t, want %#v and more", firstUsers, userCursor, done, wantUsers[:3])
	}
	secondUsers, userCursor, done, err := node.ScanUsersSlotPage(ctx, 1, userCursor, 3)
	if err != nil {
		t.Fatalf("ScanUsersSlotPage(second) error = %v", err)
	}
	if !done || !reflect.DeepEqual(userIDs(secondUsers), wantUsers[3:]) || userCursor.UID != wantUsers[3] {
		t.Fatalf("second user page=%#v cursor=%#v done=%t, want %#v and done", secondUsers, userCursor, done, wantUsers[3:])
	}

	wantChannels := append([]string(nil), ids...)
	sort.Slice(wantChannels, func(i, j int) bool {
		if len(wantChannels[i]) != len(wantChannels[j]) {
			return len(wantChannels[i]) < len(wantChannels[j])
		}
		return wantChannels[i] < wantChannels[j]
	})
	firstChannels, channelCursor, done, err := node.ScanChannelsSlotPage(ctx, 1, metadb.ChannelCursor{}, 2)
	if err != nil {
		t.Fatalf("ScanChannelsSlotPage(first) error = %v", err)
	}
	if done || !reflect.DeepEqual(channelIDs(firstChannels), wantChannels[:2]) {
		t.Fatalf("first channel page=%#v cursor=%#v done=%t, want %#v and more", firstChannels, channelCursor, done, wantChannels[:2])
	}
	secondChannels, channelCursor, done, err := node.ScanChannelsSlotPage(ctx, 1, channelCursor, 8)
	if err != nil {
		t.Fatalf("ScanChannelsSlotPage(second) error = %v", err)
	}
	if !done || !reflect.DeepEqual(channelIDs(secondChannels), wantChannels[2:]) {
		t.Fatalf("second channel page=%#v cursor=%#v done=%t, want %#v and done", secondChannels, channelCursor, done, wantChannels[2:])
	}

	firstMetas, runtimeCursor, done, err := node.ScanChannelRuntimeMetaSlotPage(ctx, 1, metadb.ChannelRuntimeMetaCursor{}, 2)
	if err != nil {
		t.Fatalf("ScanChannelRuntimeMetaSlotPage(first) error = %v", err)
	}
	if done || !reflect.DeepEqual(runtimeMetaIDs(firstMetas), wantChannels[:2]) {
		t.Fatalf("first runtime page=%#v cursor=%#v done=%t", firstMetas, runtimeCursor, done)
	}
	secondMetas, _, done, err := node.ScanChannelRuntimeMetaSlotPage(ctx, 1, runtimeCursor, 8)
	if err != nil {
		t.Fatalf("ScanChannelRuntimeMetaSlotPage(second) error = %v", err)
	}
	if !done || !reflect.DeepEqual(runtimeMetaIDs(secondMetas), wantChannels[2:]) {
		t.Fatalf("second runtime page=%#v done=%t, want %#v and done", secondMetas, done, wantChannels[2:])
	}

	repair, _, done, err := node.ListRepairScannerRuntimeMetaPage(ctx, 1, metadb.ChannelRuntimeMetaCursor{}, 8)
	if err != nil {
		t.Fatalf("ListRepairScannerRuntimeMetaPage() error = %v", err)
	}
	if !done || len(repair) != len(ids) {
		t.Fatalf("repair page len=%d done=%t, want %d and done", len(repair), done, len(ids))
	}
	for _, row := range repair {
		if want := routing.HashSlotForKey(row.Meta.ChannelID, 4); row.HashSlot != want {
			t.Fatalf("repair row %q hash slot = %d, want %d", row.Meta.ChannelID, row.HashSlot, want)
		}
	}
}

func TestNodePhysicalSlotScansFailClosedOnInvalidBoundary(t *testing.T) {
	node, _ := newLocalMetadataScanNode(t)
	if _, _, _, err := node.ScanUsersSlotPage(context.Background(), 1, metadb.UserCursor{}, 0); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ScanUsersSlotPage(limit=0) error = %v, want ErrInvalidArgument", err)
	}
	if _, _, _, err := node.ScanChannelsSlotPage(context.Background(), 99, metadb.ChannelCursor{}, 1); !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("ScanChannelsSlotPage(unknown slot) error = %v, want ErrSlotNotFound", err)
	}
	if _, _, _, err := node.ScanChannelRuntimeMetaSlotPage(context.Background(), 1, metadb.ChannelRuntimeMetaCursor{}, -1); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ScanChannelRuntimeMetaSlotPage(limit=-1) error = %v, want ErrInvalidArgument", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := node.ScanUsersSlotPage(canceled, 1, metadb.UserCursor{}, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanUsersSlotPage(canceled) error = %v, want context.Canceled", err)
	}
}

func TestPluginBindingScanCursorPaginatesAllHashSlots(t *testing.T) {
	node, db := newLocalMetadataScanNode(t)
	node.router.UpdateSlotLeaders([]routing.SlotStatus{
		{SlotID: 1, Leader: 1, LeaderTerm: 9},
		{SlotID: 2, Leader: 1, LeaderTerm: 9},
	})
	const pluginNo = "audit-plugin"
	uids := make([]string, 0, 4)
	for hashSlot := uint16(0); hashSlot < 4; hashSlot++ {
		uid := distinctChannelIDsForHashSlot(t, 4, hashSlot, 1)[0]
		uids = append(uids, uid)
		if err := db.ForHashSlot(hashSlot).BindPluginUser(context.Background(), metadb.PluginUserBinding{
			UID: uid, PluginNo: pluginNo, CreatedAtMS: int64(hashSlot + 1), UpdatedAtMS: int64(hashSlot + 10),
		}); err != nil {
			t.Fatalf("BindPluginUser(hashSlot=%d) error = %v", hashSlot, err)
		}
	}
	sort.Strings(uids)

	first, cursor, more, err := node.ListPluginBindingsByPluginNo(context.Background(), pluginNo, "", 2)
	if err != nil {
		t.Fatalf("ListPluginBindingsByPluginNo(first) error = %v", err)
	}
	if !more || cursor == "" || !reflect.DeepEqual(scannedPluginBindingUIDs(first), uids[:2]) {
		t.Fatalf("first bindings=%#v cursor=%q more=%t, want %#v and cursor", first, cursor, more, uids[:2])
	}
	decoded, err := decodePluginBindingScanCursor(cursor)
	if err != nil {
		t.Fatalf("decodePluginBindingScanCursor() error = %v", err)
	}
	if decoded.PluginNo != pluginNo || decoded.UID != uids[1] {
		t.Fatalf("decoded cursor = %#v, want plugin and last UID", decoded)
	}

	second, nextCursor, more, err := node.ListPluginBindingsByPluginNo(context.Background(), pluginNo, cursor, 2)
	if err != nil {
		t.Fatalf("ListPluginBindingsByPluginNo(second) error = %v", err)
	}
	if more || nextCursor != "" || !reflect.DeepEqual(scannedPluginBindingUIDs(second), uids[2:]) {
		t.Fatalf("second bindings=%#v cursor=%q more=%t, want %#v and done", second, nextCursor, more, uids[2:])
	}

	if _, _, _, err := node.ListPluginBindingsByPluginNo(context.Background(), "other-plugin", cursor, 2); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("plugin-mismatched cursor error = %v, want ErrInvalidArgument", err)
	}
	corrupt := base64.RawURLEncoding.EncodeToString([]byte("not-a-plugin-cursor"))
	if _, _, _, err := node.ListPluginBindingsByPluginNo(context.Background(), pluginNo, corrupt, 2); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("corrupt cursor error = %v, want ErrInvalidArgument", err)
	}
}

func TestPluginBindingScanRPCCodecRejectsUnknownVersions(t *testing.T) {
	req := pluginBindingScanRPCRequest{
		HashSlot: 3, PluginNo: "plugin", After: metadb.PluginUserBindingCursor{PluginNo: "plugin", UID: "u1"}, Limit: 8,
	}
	body, err := encodePluginBindingScanRPCRequest(req)
	if err != nil {
		t.Fatalf("encodePluginBindingScanRPCRequest() error = %v", err)
	}
	decodedReq, err := decodePluginBindingScanRPCRequest(body)
	if err != nil {
		t.Fatalf("decodePluginBindingScanRPCRequest() error = %v", err)
	}
	if decodedReq.Version != pluginBindingScanRPCVersion || decodedReq.HashSlot != req.HashSlot || decodedReq.PluginNo != req.PluginNo || decodedReq.Limit != req.Limit {
		t.Fatalf("decoded request = %#v, want versioned request", decodedReq)
	}
	if _, err := decodePluginBindingScanRPCRequest([]byte(`{"version":2}`)); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unknown request version error = %v, want ErrInvalidArgument", err)
	}

	resp := pluginBindingScanRPCResponse{
		Bindings: []metadb.PluginUserBinding{{UID: "u1", PluginNo: "plugin"}},
		Cursor:   metadb.PluginUserBindingCursor{PluginNo: "plugin", UID: "u1"}, Done: true,
	}
	body, err = encodePluginBindingScanRPCResponse(resp)
	if err != nil {
		t.Fatalf("encodePluginBindingScanRPCResponse() error = %v", err)
	}
	decodedResp, err := decodePluginBindingScanRPCResponse(body)
	if err != nil {
		t.Fatalf("decodePluginBindingScanRPCResponse() error = %v", err)
	}
	if decodedResp.Version != pluginBindingScanRPCVersion || !decodedResp.Done || !reflect.DeepEqual(decodedResp.Bindings, resp.Bindings) {
		t.Fatalf("decoded response = %#v, want versioned response", decodedResp)
	}
	if _, err := decodePluginBindingScanRPCResponse([]byte(`{"version":0}`)); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("unknown response version error = %v, want ErrInvalidArgument", err)
	}
}

func newLocalMetadataScanNode(t *testing.T) (*Node, *metadb.DB) {
	t.Helper()
	node := newStartedSlotProxyPortNode(t, &recordingProposer{})
	db, err := metadb.Open(t.TempDir())
	if err != nil {
		t.Fatalf("metadb.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("metadb.Close() error = %v", err)
		}
	})
	node.defaultSlotMetaDB = db
	return node, db
}

func userIDs(users []metadb.User) []string {
	out := make([]string, len(users))
	for index, user := range users {
		out[index] = user.UID
	}
	return out
}

func channelIDs(channels []metadb.Channel) []string {
	out := make([]string, len(channels))
	for index, channel := range channels {
		out[index] = channel.ChannelID
	}
	return out
}

func runtimeMetaIDs(metas []metadb.ChannelRuntimeMeta) []string {
	out := make([]string, len(metas))
	for index, meta := range metas {
		out[index] = meta.ChannelID
	}
	return out
}

func scannedPluginBindingUIDs(bindings []metadb.PluginUserBinding) []string {
	out := make([]string, len(bindings))
	for index, binding := range bindings {
		out[index] = binding.UID
	}
	return out
}
