package channel

import (
	"context"
	"errors"
	"strconv"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestUpsertResetsSubscribersBeforeAddingReplacement(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"old1", "old2"}, cursor: "old2", done: true},
		},
	}
	app := New(Options{Store: store})

	err := app.Upsert(context.Background(), UpsertCommand{
		Info:        Info{ChannelID: "g1", ChannelType: 2, Ban: true},
		Reset:       true,
		Subscribers: []string{"u1", "u2"},
	})

	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got, want := store.upsertChannels, []metadb.Channel{{ChannelID: "g1", ChannelType: 2, Ban: 1}}; !equalChannels(got, want) {
		t.Fatalf("upserted channels = %#v, want %#v", got, want)
	}
	if got, want := store.removeSubscribers, []subscriberCall{{channelID: "g1", channelType: 2, uids: []string{"old1", "old2"}, version: 1}}; !equalSubscriberCalls(got, want) {
		t.Fatalf("removed subscribers = %#v, want %#v", got, want)
	}
	if got, want := store.addSubscribers, []subscriberCall{{channelID: "g1", channelType: 2, uids: []string{"u1", "u2"}, version: 1}}; !equalSubscriberCalls(got, want) {
		t.Fatalf("added subscribers = %#v, want %#v", got, want)
	}
}

func TestSubscriberMutationsShareLogicalVersionsAcrossChunks(t *testing.T) {
	store := &recordingStore{
		channels: map[string]metadb.Channel{
			recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2, SubscriberMutationVersion: 7},
		},
	}
	app := New(Options{Store: store, SubscriberPageLimit: 2})

	if err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1", "u2", "u3"},
	}); err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}
	if err := app.RemoveSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u4", "u5", "u6"},
	}); err != nil {
		t.Fatalf("RemoveSubscribers() error = %v", err)
	}

	if got, want := subscriberCallVersions(store.addSubscribers), []uint64{8, 8}; !equalUint64s(got, want) {
		t.Fatalf("add versions = %#v, want %#v", got, want)
	}
	if got, want := subscriberCallVersions(store.removeSubscribers), []uint64{9, 9}; !equalUint64s(got, want) {
		t.Fatalf("remove versions = %#v, want %#v", got, want)
	}
}

func TestAddSubscribersProjectsOrdinaryMemberships(t *testing.T) {
	store := &recordingStore{
		channels: map[string]metadb.Channel{
			recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2},
		},
	}
	memberships := &recordingMembershipIndex{}
	app := New(Options{Store: store, MembershipIndex: memberships, SubscriberPageLimit: 2})

	if err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1", "u2", "u3"},
	}); err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}

	want := []membershipUpsertCall{
		{channelID: "g1", channelType: 2, uids: []string{"u1", "u2"}, joinSeq: 0},
		{channelID: "g1", channelType: 2, uids: []string{"u3"}, joinSeq: 0},
	}
	if !equalMembershipUpsertCalls(memberships.upserts, want) {
		t.Fatalf("membership upserts = %#v, want %#v", memberships.upserts, want)
	}
	for _, call := range memberships.upserts {
		if call.updatedAt <= 0 {
			t.Fatalf("membership upsert updatedAt = %d, want positive", call.updatedAt)
		}
	}
}

func TestRemoveSubscribersDeletesOrdinaryMemberships(t *testing.T) {
	store := &recordingStore{
		channels: map[string]metadb.Channel{
			recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2},
		},
	}
	memberships := &recordingMembershipIndex{}
	app := New(Options{Store: store, MembershipIndex: memberships, SubscriberPageLimit: 2})

	if err := app.RemoveSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1", "u2", "u3"},
	}); err != nil {
		t.Fatalf("RemoveSubscribers() error = %v", err)
	}

	want := []membershipDeleteCall{
		{channelID: "g1", channelType: 2, uids: []string{"u1", "u2"}},
		{channelID: "g1", channelType: 2, uids: []string{"u3"}},
	}
	if !equalMembershipDeleteCalls(memberships.deletes, want) {
		t.Fatalf("membership deletes = %#v, want %#v", memberships.deletes, want)
	}
	for _, call := range memberships.deletes {
		if call.updatedAt <= 0 {
			t.Fatalf("membership delete updatedAt = %d, want positive", call.updatedAt)
		}
	}
}

func TestInternalMemberListsDoNotProjectOrdinaryMemberships(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"old"}, cursor: "old", done: true},
		},
	}
	memberships := &recordingMembershipIndex{}
	app := New(Options{Store: store, MembershipIndex: memberships})

	if err := app.SetAllowlist(context.Background(), MemberCommand{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
		UIDs:       []string{"u1"},
	}); err != nil {
		t.Fatalf("SetAllowlist() error = %v", err)
	}

	if len(memberships.upserts) != 0 || len(memberships.deletes) != 0 {
		t.Fatalf("membership index calls = upserts %#v deletes %#v, want none", memberships.upserts, memberships.deletes)
	}
}

func TestSetAllowlistUsesStableLegacyMemberNamespace(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"old"}, cursor: "old", done: true},
		},
	}
	app := New(Options{Store: store})

	err := app.SetAllowlist(context.Background(), MemberCommand{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
		UIDs:       []string{"u1", "u2"},
	})

	if err != nil {
		t.Fatalf("SetAllowlist() error = %v", err)
	}
	if len(store.removeSubscribers) != 1 || store.removeSubscribers[0].channelID != "__wk_internal_memberlist__/allow/2/ZzE" {
		t.Fatalf("removeSubscribers = %#v, want legacy allowlist namespace", store.removeSubscribers)
	}
	if len(store.addSubscribers) != 1 || store.addSubscribers[0].channelID != "__wk_internal_memberlist__/allow/2/ZzE" {
		t.Fatalf("addSubscribers = %#v, want legacy allowlist namespace", store.addSubscribers)
	}
}

func TestAddSubscribersCreatesChannelWhenMissing(t *testing.T) {
	store := &recordingStore{getChannelErr: metadb.ErrNotFound}
	app := New(Options{Store: store})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1"},
	})

	if err != nil {
		t.Fatalf("AddSubscribers() error = %v", err)
	}
	if got, want := store.upsertChannels, []metadb.Channel{{ChannelID: "g1", ChannelType: 2}}; !equalChannels(got, want) {
		t.Fatalf("upserted channels = %#v, want %#v", got, want)
	}
}

func TestAddSubscribersReturnsLookupErrorBeforeMutating(t *testing.T) {
	store := &recordingStore{getChannelErr: errors.New("lookup failed")}
	app := New(Options{Store: store})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1"},
	})

	if err == nil || err.Error() != "lookup failed" {
		t.Fatalf("AddSubscribers() error = %v, want lookup failed", err)
	}
	if len(store.upsertChannels) != 0 || len(store.addSubscribers) != 0 {
		t.Fatalf("mutations = upsert %#v add %#v, want none", store.upsertChannels, store.addSubscribers)
	}
}

func TestListAllowlistReturnsLegacyMembers(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"u1", "u2"}, cursor: "u2", done: true},
		},
	}
	app := New(Options{Store: store})

	result, err := app.ListAllowlist(context.Background(), ChannelKey{ChannelID: "g1", ChannelType: 2})

	if err != nil {
		t.Fatalf("ListAllowlist() error = %v", err)
	}
	if got, want := result.Members, []Member{{UID: "u1"}, {UID: "u2"}}; !equalMembers(got, want) {
		t.Fatalf("members = %#v, want %#v", got, want)
	}
	if len(store.listSubscribers) != 1 || store.listSubscribers[0].channelID != "__wk_internal_memberlist__/allow/2/ZzE" {
		t.Fatalf("list subscribers = %#v, want allowlist namespace", store.listSubscribers)
	}
}

func TestListSubscribersPageValidatesStoreAndLimit(t *testing.T) {
	app := New(Options{})
	_, err := app.ListSubscribersPage(context.Background(), MemberListPageRequest{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
		Limit:      10,
	})
	if !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("ListSubscribersPage() error = %v, want %v", err, ErrStoreRequired)
	}

	app = New(Options{Store: &recordingStore{}})
	_, err = app.ListSubscribersPage(context.Background(), MemberListPageRequest{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
	})
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("ListSubscribersPage() error = %v, want %v", err, metadb.ErrInvalidArgument)
	}
}

type recordingStore struct {
	upsertChannels    []metadb.Channel
	deleteChannels    []channelKeyCall
	addSubscribers    []subscriberCall
	removeSubscribers []subscriberCall
	listSubscribers   []listSubscribersCall
	listPages         []listPage
	channels          map[string]metadb.Channel
	getChannelErr     error
}

type channelKeyCall struct {
	channelID   string
	channelType int64
}

type subscriberCall struct {
	channelID   string
	channelType int64
	uids        []string
	version     uint64
}

type listSubscribersCall struct {
	channelID   string
	channelType int64
	afterUID    string
	limit       int
}

type listPage struct {
	uids   []string
	cursor string
	done   bool
}

type recordingMembershipIndex struct {
	upserts []membershipUpsertCall
	deletes []membershipDeleteCall
}

type membershipUpsertCall struct {
	channelID   string
	channelType int64
	uids        []string
	joinSeq     uint64
	updatedAt   int64
}

type membershipDeleteCall struct {
	channelID   string
	channelType int64
	uids        []string
	updatedAt   int64
}

func (r *recordingMembershipIndex) UpsertChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) error {
	r.upserts = append(r.upserts, membershipUpsertCall{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		joinSeq:     joinSeq,
		updatedAt:   updatedAt,
	})
	return nil
}

func (r *recordingMembershipIndex) DeleteChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, updatedAt int64) error {
	r.deletes = append(r.deletes, membershipDeleteCall{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		updatedAt:   updatedAt,
	})
	return nil
}

func (r *recordingStore) UpsertChannel(_ context.Context, ch metadb.Channel) error {
	r.upsertChannels = append(r.upsertChannels, ch)
	if r.channels == nil {
		r.channels = make(map[string]metadb.Channel)
	}
	r.channels[recordingChannelKey(ch.ChannelID, ch.ChannelType)] = ch
	return nil
}

func (r *recordingStore) DeleteChannel(_ context.Context, channelID string, channelType int64) error {
	r.deleteChannels = append(r.deleteChannels, channelKeyCall{channelID: channelID, channelType: channelType})
	return nil
}

func (r *recordingStore) AddChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	version := firstMutationVersion(subscriberMutationVersion)
	r.addSubscribers = append(r.addSubscribers, subscriberCall{channelID: channelID, channelType: channelType, uids: append([]string(nil), uids...), version: version})
	r.recordSubscriberMutationVersion(channelID, channelType, version)
	return nil
}

func (r *recordingStore) RemoveChannelSubscribers(_ context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	version := firstMutationVersion(subscriberMutationVersion)
	r.removeSubscribers = append(r.removeSubscribers, subscriberCall{channelID: channelID, channelType: channelType, uids: append([]string(nil), uids...), version: version})
	r.recordSubscriberMutationVersion(channelID, channelType, version)
	return nil
}

func (r *recordingStore) ListChannelSubscribers(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	r.listSubscribers = append(r.listSubscribers, listSubscribersCall{channelID: channelID, channelType: channelType, afterUID: afterUID, limit: limit})
	if len(r.listPages) == 0 {
		return nil, afterUID, true, nil
	}
	page := r.listPages[0]
	r.listPages = r.listPages[1:]
	return append([]string(nil), page.uids...), page.cursor, page.done, nil
}

func (r *recordingStore) GetChannel(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if r.getChannelErr != nil {
		return metadb.Channel{}, r.getChannelErr
	}
	if r.channels != nil {
		if ch, ok := r.channels[recordingChannelKey(channelID, channelType)]; ok {
			return ch, nil
		}
	}
	return metadb.Channel{ChannelID: channelID, ChannelType: channelType}, nil
}

func (r *recordingStore) recordSubscriberMutationVersion(channelID string, channelType int64, version uint64) {
	if version == 0 {
		return
	}
	if r.channels == nil {
		r.channels = make(map[string]metadb.Channel)
	}
	key := recordingChannelKey(channelID, channelType)
	ch := r.channels[key]
	ch.ChannelID = channelID
	ch.ChannelType = channelType
	ch.SubscriberMutationVersion = version
	r.channels[key] = ch
}

func firstMutationVersion(values []uint64) uint64 {
	if len(values) == 0 {
		return 1
	}
	return values[0]
}

func recordingChannelKey(channelID string, channelType int64) string {
	return channelID + ":" + strconv.FormatInt(channelType, 10)
}

func subscriberCallVersions(calls []subscriberCall) []uint64 {
	out := make([]uint64, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.version)
	}
	return out
}

func equalChannels(a, b []metadb.Channel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalSubscriberCalls(a, b []subscriberCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].channelID != b[i].channelID || a[i].channelType != b[i].channelType || a[i].version != b[i].version || !equalStrings(a[i].uids, b[i].uids) {
			return false
		}
	}
	return true
}

func equalMembershipUpsertCalls(a, b []membershipUpsertCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].channelID != b[i].channelID || a[i].channelType != b[i].channelType || a[i].joinSeq != b[i].joinSeq || !equalStrings(a[i].uids, b[i].uids) {
			return false
		}
	}
	return true
}

func equalMembershipDeleteCalls(a, b []membershipDeleteCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].channelID != b[i].channelID || a[i].channelType != b[i].channelType || !equalStrings(a[i].uids, b[i].uids) {
			return false
		}
	}
	return true
}

func equalMembers(a, b []Member) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalUint64s(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
