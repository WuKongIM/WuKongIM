package plugin

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBindingUsecaseDelegatesToAuthoritativeStoreAndInvalidatesUIDCache(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot-a"] = ObservedPlugin{No: "bot-a", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 10}
	store := newFakeBindingStore()
	app := newTestBindingApp(t, rt, store, nil)

	plugin, ok, err := app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID initial: %v", err)
	}
	if ok || plugin.No != "" {
		t.Fatalf("initial selected = (%#v,%v), want none", plugin, ok)
	}
	if store.listUIDCalls != 1 {
		t.Fatalf("listUIDCalls after initial = %d, want 1", store.listUIDCalls)
	}

	result, err := app.BindPluginUser(ctx, "u1", "bot-a")
	if err != nil {
		t.Fatalf("BindPluginUser: %v", err)
	}
	if len(store.binds) != 1 || store.binds[0] != (PluginBinding{UID: "u1", PluginNo: "bot-a"}) {
		t.Fatalf("bind calls = %#v", store.binds)
	}
	if result.Binding != (PluginBinding{UID: "u1", PluginNo: "bot-a"}) || len(result.Warnings) != 0 {
		t.Fatalf("bind result = %#v", result)
	}

	plugin, ok, err = app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID after bind: %v", err)
	}
	if !ok || plugin.No != "bot-a" {
		t.Fatalf("selected = (%#v,%v), want bot-a true", plugin, ok)
	}
	if store.listUIDCalls != 2 {
		t.Fatalf("listUIDCalls after invalidated bind = %d, want 2", store.listUIDCalls)
	}

	if err := app.UnbindPluginUser(ctx, "u1", "bot-a"); err != nil {
		t.Fatalf("UnbindPluginUser: %v", err)
	}
	if len(store.unbinds) != 1 || store.unbinds[0] != (PluginBinding{UID: "u1", PluginNo: "bot-a"}) {
		t.Fatalf("unbind calls = %#v", store.unbinds)
	}

	plugin, ok, err = app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID after unbind: %v", err)
	}
	if ok || plugin.No != "" {
		t.Fatalf("selected after unbind = (%#v,%v), want none", plugin, ok)
	}
	if store.listUIDCalls != 3 {
		t.Fatalf("listUIDCalls after invalidated unbind = %d, want 3", store.listUIDCalls)
	}
}

func TestBindingUsecaseListsBindingsAndAddsWarningForAbsentLocalPlugin(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot-present"] = ObservedPlugin{No: "bot-present", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}}
	store := newFakeBindingStore()
	store.bindingsByUID["u1"] = []PluginBinding{
		{UID: "u1", PluginNo: "bot-present"},
		{UID: "u1", PluginNo: "bot-missing"},
	}
	store.page = BindingPage{
		Bindings: []PluginBinding{{UID: "u2", PluginNo: "bot-missing"}},
		Cursor:   "next",
		HasMore:  true,
	}
	app := newTestBindingApp(t, rt, store, nil)

	byUID, err := app.ListBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListBindingsByUID: %v", err)
	}
	if got, want := bindingPairs(byUID.Bindings), []PluginBinding{{UID: "u1", PluginNo: "bot-present"}, {UID: "u1", PluginNo: "bot-missing"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uid bindings = %#v, want %#v", got, want)
	}
	if got, want := warningCodes(byUID.Warnings), []string{BindingWarningPluginMissing}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uid warning codes = %#v, want %#v", got, want)
	}

	byPlugin, err := app.ListBindingsByPluginNo(ctx, "bot-missing", "cur", 50)
	if err != nil {
		t.Fatalf("ListBindingsByPluginNo: %v", err)
	}
	if store.lastPluginNo != "bot-missing" || store.lastCursor != "cur" || store.lastLimit != 50 {
		t.Fatalf("plugin list call = (%q,%q,%d)", store.lastPluginNo, store.lastCursor, store.lastLimit)
	}
	if byPlugin.Cursor != "next" || !byPlugin.HasMore {
		t.Fatalf("plugin page cursor/hasMore = (%q,%v)", byPlugin.Cursor, byPlugin.HasMore)
	}
	if got, want := warningCodes(byPlugin.Warnings), []string{BindingWarningPluginMissing}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin warning codes = %#v, want %#v", got, want)
	}
}

func TestBindingUsecaseCachesUIDBindingsAndSelectedPlugin(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["low"] = ObservedPlugin{No: "low", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 1}
	rt.plugins["high"] = ObservedPlugin{No: "high", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 10}
	store := newFakeBindingStore()
	store.bindingsByUID["u1"] = []PluginBinding{{UID: "u1", PluginNo: "low"}, {UID: "u1", PluginNo: "high"}}
	cache := NewBindingCache(BindingCacheOptions{TTL: time.Minute, MaxEntries: 8, Clock: clock})
	app := newTestBindingApp(t, rt, store, cache)

	plugin, ok, err := app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID first: %v", err)
	}
	if !ok || plugin.No != "high" {
		t.Fatalf("first selected = (%#v,%v), want high true", plugin, ok)
	}
	plugin, ok, err = app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID cached: %v", err)
	}
	if !ok || plugin.No != "high" {
		t.Fatalf("cached selected = (%#v,%v), want high true", plugin, ok)
	}
	if store.listUIDCalls != 1 {
		t.Fatalf("listUIDCalls = %d, want cached single read", store.listUIDCalls)
	}

	now = now.Add(2 * time.Minute)
	store.bindingsByUID["u1"] = []PluginBinding{{UID: "u1", PluginNo: "low"}}
	plugin, ok, err = app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID expired: %v", err)
	}
	if !ok || plugin.No != "low" {
		t.Fatalf("expired selected = (%#v,%v), want low true", plugin, ok)
	}
	if store.listUIDCalls != 2 {
		t.Fatalf("listUIDCalls after expiry = %d, want 2", store.listUIDCalls)
	}
}

func newTestBindingApp(t *testing.T, rt *fakeRuntime, store *fakeBindingStore, cache *BindingCache) *App {
	t.Helper()
	app, err := NewApp(Options{
		Runtime:      rt,
		DesiredStore: newFakeDesiredStore(),
		BindingStore: store,
		BindingCache: cache,
		Clock:        func() time.Time { return time.Unix(100, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return app
}

func bindingPairs(items []BindingDetail) []PluginBinding {
	out := make([]PluginBinding, 0, len(items))
	for _, item := range items {
		out = append(out, item.Binding)
	}
	return out
}

func warningCodes(warnings []BindingWarning) []string {
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, warning.Code)
	}
	return out
}

type fakeBindingStore struct {
	bindingsByUID map[string][]PluginBinding
	page          BindingPage
	binds         []PluginBinding
	unbinds       []PluginBinding
	listUIDCalls  int
	lastPluginNo  string
	lastCursor    string
	lastLimit     int
}

func newFakeBindingStore() *fakeBindingStore {
	return &fakeBindingStore{bindingsByUID: make(map[string][]PluginBinding)}
}

func (f *fakeBindingStore) BindPluginUser(_ context.Context, uid, pluginNo string) error {
	f.binds = append(f.binds, PluginBinding{UID: uid, PluginNo: pluginNo})
	f.bindingsByUID[uid] = append(f.bindingsByUID[uid], PluginBinding{UID: uid, PluginNo: pluginNo})
	return nil
}

func (f *fakeBindingStore) UnbindPluginUser(_ context.Context, uid, pluginNo string) error {
	f.unbinds = append(f.unbinds, PluginBinding{UID: uid, PluginNo: pluginNo})
	bindings := f.bindingsByUID[uid]
	out := bindings[:0]
	for _, binding := range bindings {
		if binding.PluginNo == pluginNo {
			continue
		}
		out = append(out, binding)
	}
	f.bindingsByUID[uid] = append([]PluginBinding(nil), out...)
	return nil
}

func (f *fakeBindingStore) ListPluginBindingsByUID(_ context.Context, uid string) ([]PluginBinding, error) {
	f.listUIDCalls++
	return append([]PluginBinding(nil), f.bindingsByUID[uid]...), nil
}

func (f *fakeBindingStore) ListPluginBindingsByPluginNo(_ context.Context, pluginNo, cursor string, limit int) (BindingPage, error) {
	f.lastPluginNo = pluginNo
	f.lastCursor = cursor
	f.lastLimit = limit
	page := f.page
	page.Bindings = append([]PluginBinding(nil), page.Bindings...)
	return page, nil
}

func (f *fakeBindingStore) ExistPluginBindingByUID(_ context.Context, uid string) (bool, error) {
	return len(f.bindingsByUID[uid]) > 0, nil
}
