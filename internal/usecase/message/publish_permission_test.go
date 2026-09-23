package message

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestPublishPermissionUsesCurrentAuthorityDespiteWarmSendCache(t *testing.T) {
	store := newFakePermissionStore()
	store.channels[permissionKey("group", 2)] = metadb.Channel{ChannelID: "group", ChannelType: 2}
	store.members[permissionKey("group", 2)] = map[string]bool{"alice": true}
	a := New(Options{PermissionStore: store, PermissionCacheTTL: time.Hour})
	// Warm the normal SEND policy's cache, then change authority without waiting.
	_, reason, err := a.checkSendPermission(context.Background(), SendCommand{FromUID: "alice", ChannelID: "group", ChannelType: 2})
	if err != nil || reason != ReasonSuccess {
		t.Fatalf("warm permission = %d, %v", reason, err)
	}
	q := PublishPermissionQuery{FromUID: "alice", TargetID: "group", TargetType: 2}
	assert := func(want Reason) {
		t.Helper()
		got, err := a.CheckPublishPermission(context.Background(), q)
		if err != nil || got != want {
			t.Fatalf("permission = %d, %v; want %d", got, err, want)
		}
	}
	assert(ReasonSuccess)
	delete(store.members[permissionKey("group", 2)], "alice")
	assert(ReasonSubscriberNotExist)
	store.members[permissionKey("group", 2)]["alice"] = true
	key := channelmembers.ChannelKey{ChannelID: "group", ChannelType: 2}
	store.members[permissionKey(channelmembers.DenylistChannelID(key), 2)] = map[string]bool{"alice": true}
	assert(ReasonInBlacklist)
	delete(store.members[permissionKey(channelmembers.DenylistChannelID(key), 2)], "alice")
	store.channels[permissionKey("alice", 1)] = metadb.Channel{SendBan: 1}
	assert(ReasonSendBan)
	delete(store.channels, permissionKey("alice", 1))
	store.hasAny[permissionKey(channelmembers.AllowlistChannelID(key), 2)] = true
	assert(ReasonNotInWhitelist)
	store.members[permissionKey(channelmembers.AllowlistChannelID(key), 2)] = map[string]bool{"alice": true}
	assert(ReasonSuccess)
	store.channels[permissionKey("group", 2)] = metadb.Channel{Ban: 1, Disband: 1}
	assert(ReasonBan)
	store.channels[permissionKey("group", 2)] = metadb.Channel{Disband: 1}
	assert(ReasonDisband)
	delete(store.channels, permissionKey("group", 2))
	assert(ReasonChannelNotExist)
}

func TestPublishPermissionNormalizesPersonWithoutSendSideEffects(t *testing.T) {
	store := newFakePermissionStore()
	hook := &recordingSendHook{}
	directory := &recordingPersonDirectoryEnsurer{}
	submitter := &recordingSubmitter{}
	a := New(Options{PermissionStore: store, PersonWhitelistEnabled: true, SendHook: hook, PersonDirectory: directory, Submitter: submitter})
	q := PublishPermissionQuery{FromUID: "alice", TargetID: "bob", TargetType: 1}
	got, err := a.CheckPublishPermission(context.Background(), q)
	if err != nil || got != ReasonNotInWhitelist {
		t.Fatalf("missing receiver allowlist = %d, %v", got, err)
	}
	store.channels[permissionKey("bob", 1)] = metadb.Channel{AllowStranger: 1}
	got, err = a.CheckPublishPermission(context.Background(), q)
	if err != nil || got != ReasonSuccess {
		t.Fatalf("allowed person = %d, %v", got, err)
	}
	canonical, err := channelid.NormalizePersonChannel("alice", "bob")
	if err != nil {
		t.Fatal(err)
	}
	store.channels[permissionKey(canonical, 1)] = metadb.Channel{Disband: 1}
	got, err = a.CheckPublishPermission(context.Background(), q)
	if err != nil || got != ReasonDisband {
		t.Fatalf("canonical terminal person = %d, %v", got, err)
	}
	if len(hook.calls) != 0 || len(directory.channelIDs) != 0 || submitter.sendCtx != nil || len(submitter.batchItems) != 0 {
		t.Fatal("permission query performed send effects")
	}
}

func TestPublishPermissionPreservesSystemPolicyWithoutDeviceBypass(t *testing.T) {
	store := newFakePermissionStore()
	store.channels[permissionKey("group", 2)] = metadb.Channel{ChannelID: "group", ChannelType: 2}
	a := New(Options{PermissionStore: store, SystemUIDs: fakeSystemUIDChecker{"system": true}, SystemDeviceID: "alice"})
	q := PublishPermissionQuery{FromUID: "alice", TargetID: "group", TargetType: 2}
	if reason, err := a.CheckPublishPermission(context.Background(), q); err != nil || reason != ReasonSubscriberNotExist {
		t.Fatalf("UID must not become DeviceID: %d, %v", reason, err)
	}
	q.FromUID = "system"
	if reason, err := a.CheckPublishPermission(context.Background(), q); err != nil || reason != ReasonSuccess {
		t.Fatalf("configured system sender = %d, %v", reason, err)
	}
	store.channels[permissionKey("group", 2)] = metadb.Channel{Disband: 1}
	if reason, err := a.CheckPublishPermission(context.Background(), q); err != nil || reason != ReasonDisband {
		t.Fatalf("system cannot bypass disband: %d, %v", reason, err)
	}
}

func TestPublishPermissionFailsClosedAndPreservesInfrastructureErrors(t *testing.T) {
	q := PublishPermissionQuery{FromUID: "alice", TargetID: "group", TargetType: 2}
	for _, a := range []*App{nil, New(Options{})} {
		if reason, err := a.CheckPublishPermission(context.Background(), q); reason != ReasonSystemError || !errors.Is(err, ErrRouteNotReady) {
			t.Fatalf("missing authority = %d, %v", reason, err)
		}
	}
	store := newFakePermissionStore()
	a := New(Options{PermissionStore: store, CommandChannelSuffix: ".cmd"})
	bad := []PublishPermissionQuery{
		{TargetID: "group", TargetType: 2}, {FromUID: "alice", TargetType: 2},
		{FromUID: "alice", TargetID: "group", TargetType: 3},
		{FromUID: "alice", TargetID: "\x00", TargetType: 2},
		{FromUID: "alice", TargetID: string([]byte{255}), TargetType: 2},
		{FromUID: strings.Repeat("a", 1025), TargetID: "group", TargetType: 2},
		{FromUID: "alice", TargetID: "alice@bob", TargetType: 1},
		{FromUID: "alice@bob", TargetID: "carol", TargetType: 1},
		{FromUID: "alice", TargetID: "group.cmd", TargetType: 2},
	}
	for _, v := range bad {
		if reason, err := a.CheckPublishPermission(context.Background(), v); reason != ReasonInvalidRequest || !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("invalid request = %d, %v", reason, err)
		}
	}
	if reason, err := a.CheckPublishPermission(nil, q); reason != ReasonInvalidRequest || !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("nil context = %d, %v", reason, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if reason, err := a.CheckPublishPermission(ctx, q); reason != ReasonSystemError || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled = %d, %v", reason, err)
	}
	if store.getChannelCalls.Load() != 0 || store.containsCalls.Load() != 0 || store.hasAnyCalls.Load() != 0 {
		t.Fatal("invalid/canceled input reached authority")
	}
	failure := errors.New("authority unavailable")
	store.channelErrs[permissionKey("alice", 1)] = failure
	if reason, err := a.CheckPublishPermission(context.Background(), q); reason != ReasonSystemError || !errors.Is(err, failure) {
		t.Fatalf("authority failure = %d, %v", reason, err)
	}
}
