package cluster

import (
	"context"
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

type fakeManagementPluginBindingNode struct {
	byPlugin     []metadb.PluginUserBinding
	cursor       string
	hasMore      bool
	lastPluginNo string
	lastCursor   string
	lastLimit    int
}

func (f *fakeManagementPluginBindingNode) ListPluginBindingsByUID(context.Context, string) ([]metadb.PluginUserBinding, error) {
	return nil, nil
}

func (f *fakeManagementPluginBindingNode) ListPluginBindingsByPluginNo(_ context.Context, pluginNo, cursor string, limit int) ([]metadb.PluginUserBinding, string, bool, error) {
	f.lastPluginNo = pluginNo
	f.lastCursor = cursor
	f.lastLimit = limit
	return append([]metadb.PluginUserBinding(nil), f.byPlugin...), f.cursor, f.hasMore, nil
}

func (f *fakeManagementPluginBindingNode) BindPluginUser(context.Context, metadb.PluginUserBinding) error {
	return nil
}

func (f *fakeManagementPluginBindingNode) UnbindPluginUser(context.Context, string, string) error {
	return nil
}

func unixMilliUTC(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}
