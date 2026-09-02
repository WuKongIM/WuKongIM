package cluster

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagementPluginBindingStoreScansByPluginNo(t *testing.T) {
	node := &fakeManagementPluginBindingNode{
		byPlugin: []metadb.PluginUserBinding{
			{UID: "u1", PluginNo: "wk.receive", CreatedAtMS: 10, UpdatedAtMS: 20},
			{UID: "u2", PluginNo: "wk.receive", CreatedAtMS: 11, UpdatedAtMS: 21},
		},
		cursor:  "cursor-2",
		hasMore: true,
	}
	store := NewManagementPluginBindingStore(node)
	var _ managementusecase.PluginBindingPluginScanner = store

	got, cursor, hasMore, err := store.ListPluginBindingsByPluginNo(context.Background(), "wk.receive", "cursor-1", 2)
	if err != nil {
		t.Fatalf("ListPluginBindingsByPluginNo() error = %v", err)
	}
	if node.lastPluginNo != "wk.receive" || node.lastCursor != "cursor-1" || node.lastLimit != 2 {
		t.Fatalf("scan args = plugin:%q cursor:%q limit:%d", node.lastPluginNo, node.lastCursor, node.lastLimit)
	}
	if cursor != "cursor-2" || !hasMore {
		t.Fatalf("cursor=%q hasMore=%t, want cursor-2 true", cursor, hasMore)
	}
	want := []managementusecase.PluginBinding{
		{UID: "u1", PluginNo: "wk.receive", CreatedAt: unixMilliUTC(10), UpdatedAt: unixMilliUTC(20)},
		{UID: "u2", PluginNo: "wk.receive", CreatedAt: unixMilliUTC(11), UpdatedAt: unixMilliUTC(21)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings = %#v, want %#v", got, want)
	}
}

func TestPluginBindingAdaptersPreserveReceiveProjectionAndManagementTimestamps(t *testing.T) {
	t.Parallel()

	node := &fakeManagementPluginBindingNode{byUID: []metadb.PluginUserBinding{{
		UID: "u1", PluginNo: "wk.receive", CreatedAtMS: 1_000, UpdatedAtMS: 2_000,
	}}}
	receiveReader := NewPluginBindingReader(node)
	receiveBindings, err := receiveReader.ListPluginBindingsByUID(context.Background(), "u1")
	if err != nil {
		t.Fatalf("receive ListPluginBindingsByUID() error = %v", err)
	}
	if node.lastUID != "u1" || len(receiveBindings) != 1 || receiveBindings[0].UID != "u1" || receiveBindings[0].PluginNo != "wk.receive" {
		t.Fatalf("receive bindings = %#v uid=%q", receiveBindings, node.lastUID)
	}

	store := NewManagementPluginBindingStore(node)
	managementBindings, err := store.ListPluginBindingsByUID(context.Background(), "u1")
	if err != nil {
		t.Fatalf("management ListPluginBindingsByUID() error = %v", err)
	}
	if len(managementBindings) != 1 || !managementBindings[0].CreatedAt.Equal(unixMilliUTC(1_000)) || !managementBindings[0].UpdatedAt.Equal(unixMilliUTC(2_000)) {
		t.Fatalf("management bindings = %#v", managementBindings)
	}

	updatedAt := time.UnixMilli(3_000).In(time.FixedZone("test", 8*60*60))
	if err := store.BindPluginUser(context.Background(), managementusecase.PluginBinding{
		UID: "u2", PluginNo: "wk.audit", UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("BindPluginUser() error = %v", err)
	}
	if node.bound.UID != "u2" || node.bound.PluginNo != "wk.audit" || node.bound.CreatedAtMS != 0 || node.bound.UpdatedAtMS != 3_000 {
		t.Fatalf("bound metadata = %#v", node.bound)
	}
	if err := store.UnbindPluginUser(context.Background(), "u2", "wk.audit"); err != nil {
		t.Fatalf("UnbindPluginUser() error = %v", err)
	}
	if node.unboundUID != "u2" || node.unboundPluginNo != "wk.audit" {
		t.Fatalf("unbind args = %q/%q", node.unboundUID, node.unboundPluginNo)
	}
}

func TestManagementPluginBindingStoreFailsClosedWithoutPluginScanner(t *testing.T) {
	t.Parallel()

	store := NewManagementPluginBindingStore(&contractPluginBindingNode{})
	_, _, _, err := store.ListPluginBindingsByPluginNo(context.Background(), "wk.receive", "", 10)
	if !errors.Is(err, managementusecase.ErrPluginBindingsUnavailable) {
		t.Fatalf("ListPluginBindingsByPluginNo() error = %v, want %v", err, managementusecase.ErrPluginBindingsUnavailable)
	}
}

type fakeManagementPluginBindingNode struct {
	byUID           []metadb.PluginUserBinding
	byPlugin        []metadb.PluginUserBinding
	cursor          string
	hasMore         bool
	lastUID         string
	lastPluginNo    string
	lastCursor      string
	lastLimit       int
	bound           metadb.PluginUserBinding
	unboundUID      string
	unboundPluginNo string
}

func (f *fakeManagementPluginBindingNode) ListPluginBindingsByUID(_ context.Context, uid string) ([]metadb.PluginUserBinding, error) {
	f.lastUID = uid
	return append([]metadb.PluginUserBinding(nil), f.byUID...), nil
}

func (f *fakeManagementPluginBindingNode) ListPluginBindingsByPluginNo(_ context.Context, pluginNo, cursor string, limit int) ([]metadb.PluginUserBinding, string, bool, error) {
	f.lastPluginNo = pluginNo
	f.lastCursor = cursor
	f.lastLimit = limit
	return append([]metadb.PluginUserBinding(nil), f.byPlugin...), f.cursor, f.hasMore, nil
}

func (f *fakeManagementPluginBindingNode) BindPluginUser(_ context.Context, binding metadb.PluginUserBinding) error {
	f.bound = binding
	return nil
}

func (f *fakeManagementPluginBindingNode) UnbindPluginUser(_ context.Context, uid, pluginNo string) error {
	f.unboundUID, f.unboundPluginNo = uid, pluginNo
	return nil
}

type contractPluginBindingNode struct{}

func (*contractPluginBindingNode) ListPluginBindingsByUID(context.Context, string) ([]metadb.PluginUserBinding, error) {
	return nil, nil
}

func (*contractPluginBindingNode) BindPluginUser(context.Context, metadb.PluginUserBinding) error {
	return nil
}

func (*contractPluginBindingNode) UnbindPluginUser(context.Context, string, string) error {
	return nil
}

func unixMilliUTC(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}
