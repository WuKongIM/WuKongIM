package plugin

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

func TestSlotBindingStoreAdapterAcceptsProxyStore(t *testing.T) {
	var _ SlotBindingStore = (*metastore.Store)(nil)
}

func TestBindingUsecaseDelegatesToAuthoritativeStoreAndInvalidatesUIDCache(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot-a"] = ObservedPlugin{No: "bot-a", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 10}
	store := newFakeBindingStore()
	cache := NewBindingCache(BindingCacheOptions{TTL: time.Minute, MaxEntries: 8, Clock: func() time.Time { return time.Unix(100, 0).UTC() }})
	app := newTestBindingApp(t, rt, store, cache)

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

func TestBindingUsecaseDoesNotCacheStaleReadAcrossConcurrentMutation(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot-a"] = ObservedPlugin{No: "bot-a", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 10}
	store := newBlockingBindingStore()
	cache := NewBindingCache(BindingCacheOptions{TTL: time.Minute, MaxEntries: 8, Clock: func() time.Time { return time.Unix(100, 0).UTC() }})
	app := newTestBindingApp(t, rt, (*fakeBindingStore)(nil), cache)
	app.bindingStore = store

	firstRead := make(chan struct{})
	releaseFirstRead := make(chan struct{})
	store.onFirstUIDList = func() {
		close(firstRead)
		<-releaseFirstRead
	}

	firstDone := make(chan error, 1)
	go func() {
		_, _, err := app.BoundReceivePluginForUID(ctx, "u1")
		firstDone <- err
	}()
	<-firstRead

	bindDone := make(chan error, 1)
	go func() {
		_, err := app.BindPluginUser(ctx, "u1", "bot-a")
		bindDone <- err
	}()
	close(releaseFirstRead)
	if err := <-firstDone; err != nil {
		t.Fatalf("first BoundReceivePluginForUID: %v", err)
	}
	if err := <-bindDone; err != nil {
		t.Fatalf("BindPluginUser: %v", err)
	}

	plugin, ok, err := app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID after concurrent bind: %v", err)
	}
	if !ok || plugin.No != "bot-a" {
		t.Fatalf("selected after concurrent bind = (%#v,%v), want bot-a true", plugin, ok)
	}
	if store.listUIDCalls != 2 {
		t.Fatalf("listUIDCalls = %d, want stale cache invalidated and reread", store.listUIDCalls)
	}
}

func TestBindingUsecaseSkipsCacheSetWhenEpochChangesAfterRead(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot-a"] = ObservedPlugin{No: "bot-a", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 10}
	store := newFakeBindingStore()
	cache := NewBindingCache(BindingCacheOptions{TTL: time.Minute, MaxEntries: 8, Clock: func() time.Time { return time.Unix(100, 0).UTC() }})
	app := newTestBindingApp(t, rt, store, cache)

	lookup, err := app.listBindingsByUID(ctx, "u1", true)
	if err != nil {
		t.Fatalf("listBindingsByUID: %v", err)
	}
	if lookup.fromCache || len(lookup.bindings) != 0 {
		t.Fatalf("lookup before bind = %#v", lookup)
	}
	if _, err := app.BindPluginUser(ctx, "u1", "bot-a"); err != nil {
		t.Fatalf("BindPluginUser: %v", err)
	}
	if app.setBindingCacheIfEpoch("u1", lookup.bindings, ObservedPlugin{}, false, lookup.epoch) {
		t.Fatal("stale lookup was cached after binding epoch changed")
	}

	plugin, ok, err := app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID: %v", err)
	}
	if !ok || plugin.No != "bot-a" {
		t.Fatalf("selected = (%#v,%v), want bot-a true", plugin, ok)
	}
	if store.listUIDCalls != 2 {
		t.Fatalf("listUIDCalls = %d, want stale cache skipped and latest reread", store.listUIDCalls)
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

func TestBindingUsecaseWarningsForPresentButNonRunnableReceivePlugins(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot-offline"] = ObservedPlugin{No: "bot-offline", Status: StatusOffline, Enabled: true, Methods: []Method{MethodReceive}}
	rt.plugins["bot-disabled"] = ObservedPlugin{No: "bot-disabled", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}}
	rt.plugins["bot-send-only"] = ObservedPlugin{No: "bot-send-only", Status: StatusRunning, Enabled: true, Methods: []Method{MethodSend}}
	desired := newFakeDesiredStore()
	desired.states["bot-disabled"] = DesiredPlugin{No: "bot-disabled", Enabled: false, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0)}
	store := newFakeBindingStore()
	store.bindingsByUID["u1"] = []PluginBinding{
		{UID: "u1", PluginNo: "bot-offline"},
		{UID: "u1", PluginNo: "bot-disabled"},
		{UID: "u1", PluginNo: "bot-send-only"},
	}
	app, err := NewApp(Options{
		Runtime:      rt,
		DesiredStore: desired,
		BindingStore: store,
		Clock:        func() time.Time { return time.Unix(100, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}

	list, err := app.ListBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListBindingsByUID: %v", err)
	}
	if got, want := warningCodes(list.Warnings), []string{BindingWarningPluginUnavailable, BindingWarningPluginDisabled, BindingWarningReceiveUnsupported}; !reflect.DeepEqual(got, want) {
		t.Fatalf("warning codes = %#v, want %#v", got, want)
	}
}

func TestBindingUsecaseListByUIDDoesNotRequireReceiveSelection(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["unrelated"] = ObservedPlugin{No: "unrelated", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}}
	store := newFakeBindingStore()
	store.bindingsByUID["u1"] = []PluginBinding{{UID: "u1", PluginNo: "missing"}}
	app, err := NewApp(Options{Runtime: rt, DesiredStore: desiredStoreError{}, BindingStore: store})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}

	list, err := app.ListBindingsByUID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListBindingsByUID: %v", err)
	}
	if got, want := bindingPairs(list.Bindings), []PluginBinding{{UID: "u1", PluginNo: "missing"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("uid bindings = %#v, want %#v", got, want)
	}
}

func TestBindingUsecaseBoundReceiveSelectionIgnoresUnrelatedDesiredStoreErrors(t *testing.T) {
	ctx := context.Background()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bound"] = ObservedPlugin{No: "bound", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 1}
	rt.plugins["unrelated"] = ObservedPlugin{No: "unrelated", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 99}
	store := newFakeBindingStore()
	store.bindingsByUID["u1"] = []PluginBinding{{UID: "u1", PluginNo: "bound"}}
	app, err := NewApp(Options{Runtime: rt, DesiredStore: selectiveDesiredStore{errors: map[string]error{"unrelated": assertAnError{}}}, BindingStore: store})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}

	plugin, ok, err := app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID: %v", err)
	}
	if !ok || plugin.No != "bound" {
		t.Fatalf("selected = (%#v,%v), want bound true", plugin, ok)
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
	rt.plugins["high"] = ObservedPlugin{No: "high", Status: StatusOffline, Enabled: true, Methods: []Method{MethodReceive}, Priority: 10}
	plugin, ok, err = app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID cached after runtime change: %v", err)
	}
	if !ok || plugin.No != "low" {
		t.Fatalf("cached after runtime change selected = (%#v,%v), want low true", plugin, ok)
	}
	if store.listUIDCalls != 1 {
		t.Fatalf("listUIDCalls after runtime revalidation = %d, want cached bindings reused", store.listUIDCalls)
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

func TestBindingUsecaseUIDBindingCacheUsesFixedTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(100, 0).UTC()
	rt := newFakeRuntime(t.TempDir())
	rt.plugins["bot-a"] = ObservedPlugin{No: "bot-a", Status: StatusRunning, Enabled: true, Methods: []Method{MethodReceive}, Priority: 1}
	store := newFakeBindingStore()
	store.bindingsByUID["u1"] = []PluginBinding{{UID: "u1", PluginNo: "bot-a"}}
	cache := NewBindingCache(BindingCacheOptions{TTL: time.Minute, MaxEntries: 8, Clock: func() time.Time { return now }})
	app := newTestBindingApp(t, rt, store, cache)

	plugin, ok, err := app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID first: %v", err)
	}
	if !ok || plugin.No != "bot-a" {
		t.Fatalf("first selected = (%#v,%v), want bot-a true", plugin, ok)
	}
	now = now.Add(50 * time.Second)
	store.bindingsByUID["u1"] = nil
	plugin, ok, err = app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID cached: %v", err)
	}
	if !ok || plugin.No != "bot-a" {
		t.Fatalf("cached selected = (%#v,%v), want bot-a true", plugin, ok)
	}
	now = now.Add(11 * time.Second)
	plugin, ok, err = app.BoundReceivePluginForUID(ctx, "u1")
	if err != nil {
		t.Fatalf("BoundReceivePluginForUID expired: %v", err)
	}
	if ok || plugin.No != "" {
		t.Fatalf("expired selected = (%#v,%v), want none", plugin, ok)
	}
	if store.listUIDCalls != 2 {
		t.Fatalf("listUIDCalls = %d, want one refresh after fixed TTL expiry", store.listUIDCalls)
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

type blockingBindingStore struct {
	mu             sync.Mutex
	bindingsByUID  map[string][]PluginBinding
	listUIDCalls   int
	onFirstUIDList func()
}

func newBlockingBindingStore() *blockingBindingStore {
	return &blockingBindingStore{bindingsByUID: make(map[string][]PluginBinding)}
}

func (f *blockingBindingStore) BindPluginUser(_ context.Context, uid, pluginNo string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bindingsByUID[uid] = append(f.bindingsByUID[uid], PluginBinding{UID: uid, PluginNo: pluginNo})
	return nil
}

func (f *blockingBindingStore) UnbindPluginUser(_ context.Context, uid, pluginNo string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *blockingBindingStore) ListPluginBindingsByUID(_ context.Context, uid string) ([]PluginBinding, error) {
	f.mu.Lock()
	f.listUIDCalls++
	onFirst := f.onFirstUIDList
	if f.listUIDCalls > 1 {
		onFirst = nil
	}
	bindings := append([]PluginBinding(nil), f.bindingsByUID[uid]...)
	f.mu.Unlock()
	if onFirst != nil {
		onFirst()
	}
	return bindings, nil
}

func (f *blockingBindingStore) ListPluginBindingsByPluginNo(context.Context, string, string, int) (BindingPage, error) {
	return BindingPage{}, nil
}

func (f *blockingBindingStore) ExistPluginBindingByUID(_ context.Context, uid string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bindingsByUID[uid]) > 0, nil
}

type desiredStoreError struct{}

func (desiredStoreError) Get(context.Context, string) (DesiredPlugin, error) {
	return DesiredPlugin{}, assertAnError{}
}

func (desiredStoreError) Save(context.Context, DesiredPlugin) error { return assertAnError{} }

func (desiredStoreError) Delete(context.Context, string) error { return assertAnError{} }

type assertAnError struct{}

func (assertAnError) Error() string { return "assertion error" }

type selectiveDesiredStore struct {
	errors map[string]error
}

func (s selectiveDesiredStore) Get(_ context.Context, no string) (DesiredPlugin, error) {
	if err := s.errors[no]; err != nil {
		return DesiredPlugin{}, err
	}
	return DesiredPlugin{}, ErrDesiredPluginNotFound
}

func (selectiveDesiredStore) Save(context.Context, DesiredPlugin) error { return assertAnError{} }

func (selectiveDesiredStore) Delete(context.Context, string) error { return assertAnError{} }
