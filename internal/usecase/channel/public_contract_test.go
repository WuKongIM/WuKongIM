package channel

import (
	"context"
	"errors"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestMetadataOperationsUseExactStoreCapabilities(t *testing.T) {
	ctx := context.Background()
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	missing := New(Options{})
	if _, err := missing.GetMetadata(ctx, key); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("GetMetadata(without store) error = %v", err)
	}
	if err := missing.CreateMetadata(ctx, Info{ChannelID: "g1", ChannelType: 2}); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("CreateMetadata(without store) error = %v", err)
	}

	base := &recordingStore{channels: map[string]metadb.Channel{
		recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2, Ban: 1},
	}}
	app := New(Options{Store: base})
	got, err := app.GetMetadata(ctx, key)
	if err != nil || got.ChannelID != "g1" || got.Ban != 1 {
		t.Fatalf("GetMetadata() = (%+v, %v)", got, err)
	}
	if err := app.CreateMetadata(ctx, Info{ChannelID: "g2", ChannelType: 2, Ban: true, Disband: true, SendBan: true}); err != nil {
		t.Fatalf("CreateMetadata() error = %v", err)
	}
	created := base.channels[recordingChannelKey("g2", 2)]
	if created.Ban != 1 || created.Disband != 1 || created.SendBan != 1 {
		t.Fatalf("created metadata = %+v", created)
	}
	if err := app.CreateMetadata(ctx, Info{ChannelID: "g2", ChannelType: 2}); !errors.Is(err, metadb.ErrAlreadyExists) {
		t.Fatalf("duplicate CreateMetadata() error = %v", err)
	}

	limited := New(Options{Store: baseStoreOnly{Store: base}})
	if err := limited.CreateMetadata(ctx, Info{ChannelID: "g3", ChannelType: 2}); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("CreateMetadata(base store) error = %v", err)
	}
	if err := limited.PatchMetadataFlags(ctx, key, BusinessFlags{Ban: true}); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("PatchMetadataFlags(base store) error = %v", err)
	}
	if err := limited.Delete(ctx, key); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("Delete(base store) error = %v", err)
	}
}

func TestDerivedMemberListOperationsKeepNamespacesAndDoNotProjectMemberships(t *testing.T) {
	ctx := context.Background()
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	memberships := &recordingMembershipIndex{}
	store := &recordingStore{}
	app := New(Options{Store: store, MembershipIndex: memberships, SubscriberPageLimit: 2})

	if err := app.AddAllowlist(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"a1", "a2", "a3"}}); err != nil {
		t.Fatalf("AddAllowlist() error = %v", err)
	}
	if err := app.RemoveAllowlist(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"a2"}}); err != nil {
		t.Fatalf("RemoveAllowlist() error = %v", err)
	}
	if err := app.AddDenylist(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"d1"}}); err != nil {
		t.Fatalf("AddDenylist() error = %v", err)
	}
	if err := app.RemoveDenylist(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"d1"}}); err != nil {
		t.Fatalf("RemoveDenylist() error = %v", err)
	}
	if err := app.SetTempSubscribers(ctx, TempSubscriberCommand{ChannelID: "tmp", UIDs: []string{"t1"}}); err != nil {
		t.Fatalf("SetTempSubscribers() error = %v", err)
	}

	const allowID = "__wk_internal_memberlist__/allow/2/ZzE"
	const denyID = "__wk_internal_memberlist__/deny/2/ZzE"
	const tempID = "__wk_internal_memberlist__/temp/8/dG1w"
	wantAddIDs := []string{allowID, allowID, denyID, tempID}
	if len(store.addSubscribers) != len(wantAddIDs) {
		t.Fatalf("add calls = %#v", store.addSubscribers)
	}
	for i, want := range wantAddIDs {
		if store.addSubscribers[i].channelID != want {
			t.Errorf("add call %d channel = %q, want %q", i, store.addSubscribers[i].channelID, want)
		}
	}
	if len(store.removeSubscribers) != 2 || store.removeSubscribers[0].channelID != allowID || store.removeSubscribers[1].channelID != denyID {
		t.Fatalf("remove calls = %#v", store.removeSubscribers)
	}
	if len(memberships.upserts) != 0 || len(memberships.deletes) != 0 {
		t.Fatalf("derived lists changed ordinary membership projection: %+v %+v", memberships.upserts, memberships.deletes)
	}

	addCount := len(store.addSubscribers)
	removeCount := len(store.removeSubscribers)
	if err := app.AddAllowlist(ctx, MemberCommand{ChannelKey: key}); err != nil {
		t.Fatal(err)
	}
	if err := app.RemoveDenylist(ctx, MemberCommand{ChannelKey: key}); err != nil {
		t.Fatal(err)
	}
	if len(store.addSubscribers) != addCount || len(store.removeSubscribers) != removeCount {
		t.Fatal("empty member mutation reached storage")
	}

	store.listPages = []listPage{{uids: []string{"old"}, done: true}}
	if err := app.SetDenylist(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"new"}}); err != nil {
		t.Fatalf("SetDenylist() error = %v", err)
	}
	store.listPages = []listPage{{uids: []string{"old"}, done: true}}
	if err := app.RemoveAllAllowlist(ctx, key); err != nil {
		t.Fatalf("RemoveAllAllowlist() error = %v", err)
	}
	if err := app.RemoveAllDenylist(ctx, key); err != nil {
		t.Fatalf("RemoveAllDenylist() error = %v", err)
	}
}

func TestPagedMemberReadsPreserveCursorAndNamespace(t *testing.T) {
	ctx := context.Background()
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	tests := []struct {
		name   string
		call   func(*App) (MemberListPageResult, error)
		wantID string
	}{
		{name: "subscribers", call: func(a *App) (MemberListPageResult, error) {
			return a.ListSubscribersPage(ctx, MemberListPageRequest{ChannelKey: key, AfterUID: "u0", Limit: 2})
		}, wantID: "g1"},
		{name: "allowlist", call: func(a *App) (MemberListPageResult, error) {
			return a.ListAllowlistPage(ctx, MemberListPageRequest{ChannelKey: key, AfterUID: "u0", Limit: 2})
		}, wantID: "__wk_internal_memberlist__/allow/2/ZzE"},
		{name: "denylist", call: func(a *App) (MemberListPageResult, error) {
			return a.ListDenylistPage(ctx, MemberListPageRequest{ChannelKey: key, AfterUID: "u0", Limit: 2})
		}, wantID: "__wk_internal_memberlist__/deny/2/ZzE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &recordingStore{listPages: []listPage{{uids: []string{"u1", "u2"}, cursor: "u2", done: false}}}
			result, err := tt.call(New(Options{Store: store}))
			if err != nil {
				t.Fatalf("page read error = %v", err)
			}
			if !equalMembers(result.Members, []Member{{UID: "u1"}, {UID: "u2"}}) || result.NextCursor != "u2" || !result.HasMore {
				t.Fatalf("page result = %+v", result)
			}
			if len(store.listSubscribers) != 1 || store.listSubscribers[0] != (listSubscribersCall{channelID: tt.wantID, channelType: 2, afterUID: "u0", limit: 2}) {
				t.Fatalf("storage page call = %+v", store.listSubscribers)
			}
		})
	}
}

func TestMembershipQueriesRequireParentAndNarrowLookupCapability(t *testing.T) {
	ctx := context.Background()
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	const allowID = "__wk_internal_memberlist__/allow/2/ZzE"
	const denyID = "__wk_internal_memberlist__/deny/2/ZzE"
	store := &recordingStore{
		channels: map[string]metadb.Channel{
			recordingChannelKey("g1", 2):    {ChannelID: "g1", ChannelType: 2},
			recordingChannelKey(allowID, 2): {ChannelID: allowID, ChannelType: 2},
			recordingChannelKey(denyID, 2):  {ChannelID: denyID, ChannelType: 2},
		},
		strictChannelLookup: true,
		containsResult:      true,
		hasResult:           true,
	}
	app := New(Options{Store: store})
	for name, call := range map[string]func() (bool, error){
		"subscriber contains":  func() (bool, error) { return app.ContainsSubscriber(ctx, key, "u1") },
		"allow contains":       func() (bool, error) { return app.ContainsAllowlistMember(ctx, key, "u1") },
		"deny contains":        func() (bool, error) { return app.ContainsDenylistMember(ctx, key, "u1") },
		"subscribers nonempty": func() (bool, error) { return app.HasSubscribers(ctx, key) },
		"allow nonempty":       func() (bool, error) { return app.HasAllowlist(ctx, key) },
		"deny nonempty":        func() (bool, error) { return app.HasDenylist(ctx, key) },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := call()
			if err != nil || !got {
				t.Fatalf("query = (%v, %v), want true", got, err)
			}
		})
	}
	containsIDs := map[string]bool{}
	for _, call := range store.containsCalls {
		containsIDs[call.channelID] = true
	}
	if len(store.containsCalls) != 3 || !containsIDs["g1"] || !containsIDs[allowID] || !containsIDs[denyID] {
		t.Fatalf("contains calls = %+v", store.containsCalls)
	}
	hasIDs := map[string]bool{}
	for _, call := range store.hasCalls {
		hasIDs[call.channelID] = true
	}
	if len(store.hasCalls) != 3 || !hasIDs["g1"] || !hasIDs[allowID] || !hasIDs[denyID] {
		t.Fatalf("has calls = %+v", store.hasCalls)
	}

	missingDerived := &recordingStore{channels: map[string]metadb.Channel{
		recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2},
	}, strictChannelLookup: true}
	missingApp := New(Options{Store: missingDerived})
	if got, err := missingApp.ContainsAllowlistMember(ctx, key, "u1"); err != nil || got {
		t.Fatalf("ContainsAllowlistMember(missing list) = (%v, %v)", got, err)
	}
	if got, err := missingApp.HasDenylist(ctx, key); err != nil || got {
		t.Fatalf("HasDenylist(missing list) = (%v, %v)", got, err)
	}

	limited := New(Options{Store: baseStoreOnly{Store: store}})
	if _, err := limited.ContainsSubscriber(ctx, key, "u1"); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("ContainsSubscriber(base store) error = %v", err)
	}
	if _, err := limited.HasSubscribers(ctx, key); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("HasSubscribers(base store) error = %v", err)
	}
}

func TestCountedDenylistAndRemoveAllSubscriberContracts(t *testing.T) {
	ctx := context.Background()
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	store := &recordingStore{
		channels:            map[string]metadb.Channel{recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2}},
		strictChannelLookup: true,
		countedAddResult:    metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 2},
		countedRemoveResult: metadb.SubscriberMutationResult{RequestedCount: 1, ChangedCount: 1},
	}
	app := New(Options{Store: store})
	if result, err := app.MutateDenylistCounted(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"u1", "u2"}}, true); err != nil || result != store.countedAddResult {
		t.Fatalf("MutateDenylistCounted(add) = (%+v, %v)", result, err)
	}
	if result, err := app.MutateDenylistCounted(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"u1"}}, false); err != nil || result != store.countedRemoveResult {
		t.Fatalf("MutateDenylistCounted(remove) = (%+v, %v)", result, err)
	}

	missing := &recordingStore{channels: map[string]metadb.Channel{recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2}}, strictChannelLookup: true}
	result, err := New(Options{Store: missing}).MutateDenylistCounted(ctx, MemberCommand{ChannelKey: key, UIDs: []string{"u1", "u2"}}, false)
	if err != nil || result.RequestedCount != 2 || result.ChangedCount != 0 {
		t.Fatalf("remove missing denylist = (%+v, %v)", result, err)
	}

	ordinary := &recordingStore{
		channels:  map[string]metadb.Channel{recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2, SubscriberCount: 2}},
		listPages: []listPage{{uids: []string{"u1", "u2"}, done: true}},
	}
	memberships := &recordingMembershipIndex{}
	observer := &recordingSubscriberMutationObserver{}
	ordinaryApp := New(Options{Store: ordinary, MembershipIndex: memberships, SubscriberMutationObserver: observer})
	if err := ordinaryApp.RemoveAllSubscribers(ctx, key); err != nil {
		t.Fatalf("RemoveAllSubscribers() error = %v", err)
	}
	if len(memberships.deletes) != 1 || len(observer.events) != 1 || !observer.events[0].Reset {
		t.Fatalf("remove-all projections=%+v events=%+v", memberships.deletes, observer.events)
	}
}

func TestResetToEmptyAndCountedRemovalKeepReverseIndexCoherent(t *testing.T) {
	ctx := context.Background()
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	store := &recordingStore{
		channels: map[string]metadb.Channel{recordingChannelKey("g1", 2): {
			ChannelID: "g1", ChannelType: 2, SubscriberCount: 2, SubscriberMutationVersion: 4,
		}},
		listPages:           []listPage{{uids: []string{"u1", "u2"}, done: true}},
		countedRemoveResult: metadb.SubscriberMutationResult{RequestedCount: 1, ChangedCount: 1},
	}
	memberships := &recordingMembershipIndex{}
	observer := &recordingSubscriberMutationObserver{}
	app := New(Options{Store: store, MembershipIndex: memberships, SubscriberMutationObserver: observer})
	if err := app.AddSubscribers(ctx, SubscriberCommand{ChannelID: "g1", ChannelType: 2, Reset: true}); err != nil {
		t.Fatalf("AddSubscribers(reset empty) error = %v", err)
	}
	if len(memberships.deletes) != 1 || len(observer.events) != 1 || !observer.events[0].Reset || len(observer.events[0].AddedUIDs) != 0 {
		t.Fatalf("reset projections=%+v events=%+v", memberships.deletes, observer.events)
	}

	result, err := app.MutateSubscribersCounted(ctx, SubscriberCommand{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u3"}}, false)
	if err != nil || result != store.countedRemoveResult {
		t.Fatalf("MutateSubscribersCounted(remove) = (%+v, %v)", result, err)
	}
	if len(memberships.deletes) != 2 || !equalStrings(memberships.deletes[1].uids, []string{"u3"}) {
		t.Fatalf("counted removal projection = %+v", memberships.deletes)
	}
	if len(observer.events) != 2 || !equalStrings(observer.events[1].RemovedUIDs, []string{"u3"}) {
		t.Fatalf("counted removal observer events = %+v", observer.events)
	}

	if _, err := New(Options{}).ListAllowlist(ctx, key); !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("ListAllowlist(without store) error = %v", err)
	}
}

func TestMembershipQueriesAndChunkingFailBeforePartialMutation(t *testing.T) {
	ctx := context.Background()
	key := ChannelKey{ChannelID: "missing", ChannelType: 2}
	store := &recordingStore{channels: map[string]metadb.Channel{}, strictChannelLookup: true}
	app := New(Options{Store: store, SubscriberPageLimit: 2})
	if _, err := app.ContainsSubscriber(ctx, key, "u1"); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("ContainsSubscriber(missing parent) error = %v", err)
	}
	if _, err := app.HasSubscribers(ctx, key); !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("HasSubscribers(missing parent) error = %v", err)
	}
	if len(store.containsCalls) != 0 || len(store.hasCalls) != 0 {
		t.Fatal("missing parent reached subscriber lookup")
	}

	wantErr := errors.New("chunk rejected")
	calls := 0
	err := app.forEachSubscriberChunk([]string{"u1", "u2", "u3"}, func([]string) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) || calls != 1 {
		t.Fatalf("forEachSubscriberChunk() = calls %d, error %v", calls, err)
	}
	if got := namespacedListChannelID(memberListKind("unknown"), key); got != "" {
		t.Fatalf("unknown member-list namespace = %q", got)
	}
}

type baseStoreOnly struct{ Store }
