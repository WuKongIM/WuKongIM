package app

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerChannelBusinessOperatorPreservesMetadataAndMemberContracts(t *testing.T) {
	store := newManagerChannelContractStore()
	operator := newManagerChannelBusinessOperator(channelusecase.New(channelusecase.Options{
		Store: store,
	}))
	if operator == nil {
		t.Fatal("newManagerChannelBusinessOperator() = nil")
	}
	ctx := context.Background()
	key := managementusecase.BusinessChannelKey{ChannelID: "group-contract", ChannelType: 2}

	err := operator.CreateMetadata(ctx, managementusecase.BusinessChannelInfo{
		BusinessChannelKey: key,
		Ban:                true,
		Disband:            false,
		SendBan:            true,
	})
	if err != nil {
		t.Fatalf("CreateMetadata() error = %v", err)
	}
	metadata, err := operator.GetMetadata(ctx, key)
	if err != nil {
		t.Fatalf("GetMetadata() error = %v", err)
	}
	if metadata.ChannelID != key.ChannelID || metadata.ChannelType != 2 || metadata.Ban != 1 || metadata.Disband != 0 || metadata.SendBan != 1 {
		t.Fatalf("GetMetadata() = %#v, want exact Manager DTO fields", metadata)
	}

	if err := operator.PatchMetadataFlags(ctx, key, managementusecase.BusinessChannelFlags{Disband: true}); err != nil {
		t.Fatalf("PatchMetadataFlags() error = %v", err)
	}
	metadata, err = operator.GetMetadata(ctx, key)
	if err != nil {
		t.Fatalf("GetMetadata() after patch error = %v", err)
	}
	if metadata.Ban != 0 || metadata.Disband != 1 || metadata.SendBan != 0 {
		t.Fatalf("patched flags = (%d,%d,%d), want (0,1,0)", metadata.Ban, metadata.Disband, metadata.SendBan)
	}

	ordinary, err := operator.MutateSubscribersCounted(ctx, key, []string{"uid-c", "uid-a", "uid-b", "uid-a"}, true)
	if err != nil {
		t.Fatalf("MutateSubscribersCounted(add) error = %v", err)
	}
	if ordinary != (metadb.SubscriberMutationResult{RequestedCount: 3, ChangedCount: 3}) {
		t.Fatalf("ordinary add result = %#v, want distinct requested and changed counts", ordinary)
	}
	allow, err := operator.MutateAllowlistCounted(ctx, key, []string{"uid-b", "uid-a"}, true)
	if err != nil {
		t.Fatalf("MutateAllowlistCounted(add) error = %v", err)
	}
	if allow != (metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 2}) {
		t.Fatalf("allowlist add result = %#v", allow)
	}
	deny, err := operator.MutateDenylistCounted(ctx, key, []string{"uid-z"}, true)
	if err != nil {
		t.Fatalf("MutateDenylistCounted(add) error = %v", err)
	}
	if deny != (metadb.SubscriberMutationResult{RequestedCount: 1, ChangedCount: 1}) {
		t.Fatalf("denylist add result = %#v", deny)
	}

	for name, check := range map[string]func(context.Context, managementusecase.BusinessChannelKey) (bool, error){
		"subscribers": operator.HasSubscribers,
		"allowlist":   operator.HasAllowlist,
		"denylist":    operator.HasDenylist,
	} {
		present, err := check(ctx, key)
		if err != nil || !present {
			t.Fatalf("Has%s() = %v, %v; want true, nil", name, present, err)
		}
	}
	for name, check := range map[string]func(context.Context, managementusecase.BusinessChannelKey, string) (bool, error){
		"subscriber": operator.ContainsSubscriber,
		"allowlist":  operator.ContainsAllowlistMember,
		"denylist":   operator.ContainsDenylistMember,
	} {
		uid := "uid-a"
		if name == "denylist" {
			uid = "uid-z"
		}
		present, err := check(ctx, key, uid)
		if err != nil || !present {
			t.Fatalf("Contains%s(%q) = %v, %v; want true, nil", name, uid, present, err)
		}
	}

	assertManagerChannelMemberPage(t, operator.ListSubscribersPage, ctx, key, "", 2,
		managementusecase.BusinessChannelMemberPageResult{UIDs: []string{"uid-a", "uid-b"}, NextCursor: "uid-b", HasMore: true})
	assertManagerChannelMemberPage(t, operator.ListSubscribersPage, ctx, key, "uid-b", 2,
		managementusecase.BusinessChannelMemberPageResult{UIDs: []string{"uid-c"}, HasMore: false})
	assertManagerChannelMemberPage(t, operator.ListAllowlistPage, ctx, key, "", 10,
		managementusecase.BusinessChannelMemberPageResult{UIDs: []string{"uid-a", "uid-b"}, HasMore: false})
	assertManagerChannelMemberPage(t, operator.ListDenylistPage, ctx, key, "", 10,
		managementusecase.BusinessChannelMemberPageResult{UIDs: []string{"uid-z"}, HasMore: false})

	removed, err := operator.MutateSubscribersCounted(ctx, key, []string{"uid-a", "uid-missing"}, false)
	if err != nil {
		t.Fatalf("MutateSubscribersCounted(remove) error = %v", err)
	}
	if removed != (metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 1}) {
		t.Fatalf("ordinary remove result = %#v, want requested=2 changed=1", removed)
	}
	removed, err = operator.MutateAllowlistCounted(ctx, key, []string{"uid-b"}, false)
	if err != nil || removed.ChangedCount != 1 {
		t.Fatalf("MutateAllowlistCounted(remove) = %#v, %v", removed, err)
	}
	removed, err = operator.MutateDenylistCounted(ctx, key, []string{"uid-z"}, false)
	if err != nil || removed.ChangedCount != 1 {
		t.Fatalf("MutateDenylistCounted(remove) = %#v, %v", removed, err)
	}
}

func TestManagerChannelBusinessOperatorMapsAuthorityFailureForEveryOperationShape(t *testing.T) {
	cause := clusterpkg.ErrRouteNotReady
	operator := newManagerChannelBusinessOperator(channelusecase.New(channelusecase.Options{
		Store: &managerChannelAuthorityErrorStore{err: cause},
	}))
	ctx := context.Background()
	key := managementusecase.BusinessChannelKey{ChannelID: "group-authority", ChannelType: 2}
	info := managementusecase.BusinessChannelInfo{BusinessChannelKey: key}
	flags := managementusecase.BusinessChannelFlags{Ban: true}
	page := managementusecase.BusinessChannelMemberPageRequest{BusinessChannelKey: key, Limit: 10}

	checks := map[string]func() error{
		"get metadata":        func() error { _, err := operator.GetMetadata(ctx, key); return err },
		"create metadata":     func() error { return operator.CreateMetadata(ctx, info) },
		"patch flags":         func() error { return operator.PatchMetadataFlags(ctx, key, flags) },
		"has subscribers":     func() error { _, err := operator.HasSubscribers(ctx, key); return err },
		"has allowlist":       func() error { _, err := operator.HasAllowlist(ctx, key); return err },
		"has denylist":        func() error { _, err := operator.HasDenylist(ctx, key); return err },
		"contains subscriber": func() error { _, err := operator.ContainsSubscriber(ctx, key, "uid-a"); return err },
		"contains allowlist":  func() error { _, err := operator.ContainsAllowlistMember(ctx, key, "uid-a"); return err },
		"contains denylist":   func() error { _, err := operator.ContainsDenylistMember(ctx, key, "uid-a"); return err },
		"list subscribers":    func() error { _, err := operator.ListSubscribersPage(ctx, page); return err },
		"list allowlist":      func() error { _, err := operator.ListAllowlistPage(ctx, page); return err },
		"list denylist":       func() error { _, err := operator.ListDenylistPage(ctx, page); return err },
		"mutate subscribers": func() error {
			_, err := operator.MutateSubscribersCounted(ctx, key, []string{"uid-a"}, true)
			return err
		},
		"mutate allowlist": func() error { _, err := operator.MutateAllowlistCounted(ctx, key, []string{"uid-a"}, true); return err },
		"mutate denylist":  func() error { _, err := operator.MutateDenylistCounted(ctx, key, []string{"uid-a"}, true); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			err := check()
			if !errors.Is(err, managementusecase.ErrBusinessChannelAuthorityUnavailable) || !errors.Is(err, cause) {
				t.Fatalf("error = %v, want authority-unavailable wrapping %v", err, cause)
			}
		})
	}
}

func assertManagerChannelMemberPage(
	t *testing.T,
	list func(context.Context, managementusecase.BusinessChannelMemberPageRequest) (managementusecase.BusinessChannelMemberPageResult, error),
	ctx context.Context,
	key managementusecase.BusinessChannelKey,
	after string,
	limit int,
	want managementusecase.BusinessChannelMemberPageResult,
) {
	t.Helper()
	got, err := list(ctx, managementusecase.BusinessChannelMemberPageRequest{
		BusinessChannelKey: key,
		AfterUID:           after,
		Limit:              limit,
	})
	if err != nil {
		t.Fatalf("list page error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("list page = %#v, want %#v", got, want)
	}
}

type managerChannelContractStore struct {
	channels map[string]metadb.Channel
	members  map[string][]string
}

func newManagerChannelContractStore() *managerChannelContractStore {
	return &managerChannelContractStore{
		channels: make(map[string]metadb.Channel),
		members:  make(map[string][]string),
	}
}

func (s *managerChannelContractStore) GetChannel(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	channel, ok := s.channels[managerChannelBusinessStoreKey(channelID, channelType)]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return channel, nil
}

func (s *managerChannelContractStore) UpsertChannel(_ context.Context, channel metadb.Channel) error {
	s.channels[managerChannelBusinessStoreKey(channel.ChannelID, channel.ChannelType)] = channel
	return nil
}

func (s *managerChannelContractStore) CreateChannelStrict(ctx context.Context, channel metadb.Channel) error {
	if _, err := s.GetChannel(ctx, channel.ChannelID, channel.ChannelType); err == nil {
		return metadb.ErrAlreadyExists
	} else if !errors.Is(err, metadb.ErrNotFound) {
		return err
	}
	return s.UpsertChannel(ctx, channel)
}

func (s *managerChannelContractStore) PatchChannelBusinessFlags(ctx context.Context, channelID string, channelType int64, flags metadb.ChannelBusinessFlags) error {
	channel, err := s.GetChannel(ctx, channelID, channelType)
	if err != nil {
		return err
	}
	channel.Ban = flags.Ban
	channel.Disband = flags.Disband
	channel.SendBan = flags.SendBan
	return s.UpsertChannel(ctx, channel)
}

func (s *managerChannelContractStore) AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, version ...uint64) error {
	_, err := s.AddChannelSubscribersCounted(ctx, channelID, channelType, uids, version...)
	return err
}

func (s *managerChannelContractStore) RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, version ...uint64) error {
	_, err := s.RemoveChannelSubscribersCounted(ctx, channelID, channelType, uids, version...)
	return err
}

func (s *managerChannelContractStore) AddChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, version ...uint64) (metadb.SubscriberMutationResult, error) {
	return s.mutate(ctx, channelID, channelType, uids, true, version...)
}

func (s *managerChannelContractStore) RemoveChannelSubscribersCounted(ctx context.Context, channelID string, channelType int64, uids []string, version ...uint64) (metadb.SubscriberMutationResult, error) {
	return s.mutate(ctx, channelID, channelType, uids, false, version...)
}

func (s *managerChannelContractStore) mutate(ctx context.Context, channelID string, channelType int64, uids []string, add bool, version ...uint64) (metadb.SubscriberMutationResult, error) {
	key := managerChannelBusinessStoreKey(channelID, channelType)
	requested := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		requested[uid] = struct{}{}
	}
	current := make(map[string]struct{}, len(s.members[key]))
	for _, uid := range s.members[key] {
		current[uid] = struct{}{}
	}
	changed := 0
	for uid := range requested {
		_, exists := current[uid]
		if add && !exists {
			current[uid] = struct{}{}
			changed++
		}
		if !add && exists {
			delete(current, uid)
			changed++
		}
	}
	members := make([]string, 0, len(current))
	for uid := range current {
		members = append(members, uid)
	}
	sort.Strings(members)
	s.members[key] = members
	channel, err := s.GetChannel(ctx, channelID, channelType)
	if err != nil {
		return metadb.SubscriberMutationResult{}, err
	}
	channel.SubscriberCount = uint64(len(members))
	if len(version) > 0 {
		channel.SubscriberMutationVersion = version[0]
	}
	if err := s.UpsertChannel(ctx, channel); err != nil {
		return metadb.SubscriberMutationResult{}, err
	}
	return metadb.SubscriberMutationResult{RequestedCount: len(requested), ChangedCount: changed}, nil
}

func (s *managerChannelContractStore) ListChannelSubscribers(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	members := s.members[managerChannelBusinessStoreKey(channelID, channelType)]
	start := sort.SearchStrings(members, afterUID)
	for start < len(members) && members[start] <= afterUID {
		start++
	}
	end := start + limit
	if end > len(members) {
		end = len(members)
	}
	page := append([]string(nil), members[start:end]...)
	done := end == len(members)
	next := ""
	if !done && len(page) > 0 {
		next = page[len(page)-1]
	}
	return page, next, done, nil
}

func (s *managerChannelContractStore) ContainsChannelSubscriber(_ context.Context, channelID string, channelType int64, uid string) (bool, error) {
	members := s.members[managerChannelBusinessStoreKey(channelID, channelType)]
	index := sort.SearchStrings(members, uid)
	return index < len(members) && members[index] == uid, nil
}

func (s *managerChannelContractStore) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	return len(s.members[managerChannelBusinessStoreKey(channelID, channelType)]) > 0, nil
}

type managerChannelAuthorityErrorStore struct {
	err error
}

func (s *managerChannelAuthorityErrorStore) GetChannel(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, s.err
}

func (s *managerChannelAuthorityErrorStore) UpsertChannel(context.Context, metadb.Channel) error {
	return s.err
}

func (s *managerChannelAuthorityErrorStore) CreateChannelStrict(context.Context, metadb.Channel) error {
	return s.err
}

func (s *managerChannelAuthorityErrorStore) PatchChannelBusinessFlags(context.Context, string, int64, metadb.ChannelBusinessFlags) error {
	return s.err
}

func (s *managerChannelAuthorityErrorStore) AddChannelSubscribers(context.Context, string, int64, []string, ...uint64) error {
	return s.err
}

func (s *managerChannelAuthorityErrorStore) RemoveChannelSubscribers(context.Context, string, int64, []string, ...uint64) error {
	return s.err
}

func (s *managerChannelAuthorityErrorStore) AddChannelSubscribersCounted(context.Context, string, int64, []string, ...uint64) (metadb.SubscriberMutationResult, error) {
	return metadb.SubscriberMutationResult{}, s.err
}

func (s *managerChannelAuthorityErrorStore) RemoveChannelSubscribersCounted(context.Context, string, int64, []string, ...uint64) (metadb.SubscriberMutationResult, error) {
	return metadb.SubscriberMutationResult{}, s.err
}

func (s *managerChannelAuthorityErrorStore) ListChannelSubscribers(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return nil, "", false, s.err
}

func (s *managerChannelAuthorityErrorStore) ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error) {
	return false, s.err
}

func (s *managerChannelAuthorityErrorStore) HasChannelSubscribers(context.Context, string, int64) (bool, error) {
	return false, s.err
}
