package message

import (
	"context"
	"fmt"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// Failure cases: skipping plugin hooks must not skip mandatory authorization;
// warm auxiliary facts must not retain bans or grants; clearing either scope
// must preserve the other scope; explicit recipients still require UID policy.
func TestSendBanSkipPluginHooksBoundary(t *testing.T) {
	for _, skip := range []bool{false, true} {
		for _, batch := range []bool{false, true} {
			for _, target := range []string{"group", "info", "explicit-recipients"} {
				t.Run(fmt.Sprintf("skip=%t/batch=%t/%s", skip, batch, target), func(t *testing.T) {
					base := newFakePermissionStore()
					store := &recordingPermissionBatchStore{base: base}
					cmd := SendCommand{FromUID: "u", ChannelID: "target", ChannelType: channelTypeGroup, Payload: []byte("original"), SkipPluginHooks: skip}
					if target == "info" {
						cmd.ChannelType = channelTypeInfo
					}
					if target == "explicit-recipients" {
						cmd.RequestScoped = true
						cmd.MessageScopedUIDs = []string{"v"}
					}
					key := permissionKey(cmd.ChannelID, int64(cmd.ChannelType))
					base.members[key] = map[string]bool{"u": true}
					hook := &recordingSendHook{mutate: func(c SendCommand) (SendCommand, Reason, error) {
						c.Payload = []byte("hooked")
						return c, ReasonSuccess, nil
					}}
					submitter := &recordingSubmitter{}
					app := New(Options{PermissionStore: store, PermissionBatchStore: store, PermissionCacheTTL: time.Hour, SendHook: hook, Submitter: submitter})
					for _, state := range []struct {
						name          string
						user, channel int64
					}{
						{"warm", 0, 0}, {"user-only", 1, 0}, {"both", 1, 1},
						{"clear-user-first", 0, 1}, {"clear-channel-last", 0, 0},
						{"both-again", 1, 1}, {"clear-channel-first", 1, 0}, {"clear-user-last", 0, 0},
					} {
						t.Run(state.name, func(t *testing.T) {
							base.userPolicies["u"] = metadb.SendBanResult{SendBan: state.user}
							base.channels[key] = metadb.Channel{ChannelID: cmd.ChannelID, ChannelType: int64(cmd.ChannelType), SendBan: state.channel}
							hook.calls = nil
							ok := SendResult{MessageID: 60, MessageSeq: 1, Reason: ReasonSuccess}
							*submitter = recordingSubmitter{sendResult: ok, batchResults: []SendBatchItemResult{{Result: ok}, {Result: ok}}}
							var results []SendBatchItemResult
							if batch {
								results = app.SendBatch([]SendBatchItem{{Command: cmd}, {Command: cmd}})
								require.Len(t, results, 2)
							} else {
								r, err := app.Send(context.Background(), cmd)
								results = []SendBatchItemResult{{Result: r, Err: err}}
							}
							denied := state.user == 1 || (!cmd.RequestScoped && state.channel == 1)
							for _, r := range results {
								require.NoError(t, r.Err)
								if denied {
									require.Equal(t, ReasonSendBan, r.Result.Reason)
									require.Zero(t, r.Result.MessageID)
									require.Zero(t, r.Result.MessageSeq)
								} else {
									require.Equal(t, ok, r.Result)
								}
							}
							if denied {
								require.Empty(t, submitter.sendCommand.FromUID)
								require.Empty(t, submitter.batchItems)
							}
							if denied || skip {
								require.Empty(t, hook.calls)
							} else {
								require.Len(t, hook.calls, len(results))
							}
							if !denied {
								wantPayload := "hooked"
								if skip {
									wantPayload = "original"
								}
								if batch {
									require.Len(t, submitter.batchItems, 1)
									for _, item := range submitter.batchItems[0] {
										require.Equal(t, wantPayload, string(item.Command.Payload))
									}
								} else {
									require.Equal(t, wantPayload, string(submitter.sendCommand.Payload))
								}
							}
						})
					}
				})
			}
		}
	}
}

// Failure cases: cached user/channel facts, trusted-system and explicit-recipient
// bypasses, duplicate user reads across channel types, scalar fallback inside a
// batch, and missing fact results must not admit a restricted sender.
func TestSendBanUnifiedBatchFacts(t *testing.T) {
	facts := &banPermissionFacts{}
	app := New(Options{PermissionStore: facts, PermissionBatchStore: facts, PermissionCacheTTL: time.Hour, SystemUIDs: fakeSystemUIDChecker{"u": true}})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u", ChannelID: "g", ChannelType: 2}},
		{Command: SendCommand{FromUID: "u", ChannelID: "g2", ChannelType: 2}},
		{Command: SendCommand{FromUID: "u", ChannelID: "v", ChannelType: 1, NormalizePersonChannel: true}},
		{Command: SendCommand{FromUID: "u", RequestScoped: true, MessageScopedUIDs: []string{"v"}}},
	}
	results := app.SendBatch(items)
	require.Len(t, results, 4)
	for _, r := range results {
		require.NoError(t, r.Err)
		require.Equal(t, ReasonSendBan, r.Result.Reason)
	}
	require.Equal(t, 1, facts.calls)
	userReads := 0
	for _, r := range facts.reads {
		if r.Kind == PermissionReadUserSendPolicy {
			userReads++
		}
	}
	require.Equal(t, 1, userReads)
	facts.userBan = 0
	facts.channelBan = 1
	facts.allowUser = true
	result, err := app.Send(context.Background(), SendCommand{FromUID: "u", ChannelID: "g", ChannelType: 2})
	require.NoError(t, err)
	require.Equal(t, ReasonSendBan, result.Reason)
	facts.short = true
	_, err = app.Send(context.Background(), SendCommand{FromUID: "u", ChannelID: "g", ChannelType: 2})
	require.Error(t, err)
}

type banPermissionFacts struct {
	calls               int
	reads               []PermissionRead
	userBan, channelBan int64
	allowUser, short    bool
}

func (f *banPermissionFacts) ReadPermissionsBatch(_ context.Context, reads []PermissionRead) []PermissionReadResult {
	f.calls++
	f.reads = append([]PermissionRead(nil), reads...)
	out := make([]PermissionReadResult, len(reads))
	if f.short {
		return nil
	}
	for i, r := range reads {
		if r.Kind == PermissionReadUserSendPolicy {
			ban := int64(1)
			if f.allowUser {
				ban = f.userBan
			}
			out[i] = PermissionReadResult{Found: true, UserPolicy: metadb.SendBanResult{SendBan: ban}}
		} else {
			out[i] = PermissionReadResult{Found: true, Value: true, Channel: metadb.Channel{ChannelID: r.ChannelID, ChannelType: r.ChannelType, SendBan: f.channelBan}}
		}
	}
	return out
}
func (f *banPermissionFacts) GetChannelForPermission(context.Context, string, int64) (metadb.Channel, error) {
	panic("unexpected scalar read")
}
func (f *banPermissionFacts) ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error) {
	panic("unexpected scalar read")
}
func (f *banPermissionFacts) HasChannelSubscribers(context.Context, string, int64) (bool, error) {
	panic("unexpected scalar read")
}

// Auxiliary TTL must reduce membership reads without caching mandatory policy;
// independent cancellation must not poison a sibling sharing the same deadline.
func TestSendBanBatchCacheAndCancellationBoundaries(t *testing.T) {
	base := newFakePermissionStore()
	base.channels[permissionKey("g", 2)] = metadb.Channel{ChannelID: "g", ChannelType: 2}
	base.members[permissionKey("g", 2)] = map[string]bool{"u": true}
	store := &recordingPermissionBatchStore{base: base}
	app := New(Options{PermissionStore: store, PermissionBatchStore: store, PermissionCacheTTL: time.Hour})
	cmd := SendCommand{FromUID: "u", ChannelID: "g", ChannelType: 2}
	_, reason, err := app.checkSendPermission(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, ReasonSuccess, reason)
	require.Len(t, store.reads, 6)
	base.channels[permissionKey("g", 2)] = metadb.Channel{ChannelID: "g", ChannelType: 2, SendBan: 1}
	_, reason, err = app.checkSendPermission(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, ReasonSendBan, reason)
	require.Len(t, store.reads, 2)
	base.channels[permissionKey("g", 2)] = metadb.Channel{ChannelID: "g", ChannelType: 2}
	_, reason, err = app.checkSendPermission(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, ReasonSuccess, reason)
	require.Len(t, store.reads, 2)
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	live, stop := context.WithCancel(context.Background())
	defer stop()
	deadlines := &deadlinePermissionBatchStore{recordingPermissionBatchStore: store}
	app = New(Options{PermissionStore: deadlines, PermissionBatchStore: deadlines})
	items := []SendBatchItem{{Context: expired, Command: cmd}, {Context: live, Command: cmd}}
	groups := []sendBatchPermissionGroup{{representative: 0, indexes: []int{0}}, {representative: 1, indexes: []int{1}}}
	results := app.resolveSendBatchPermissions(items, groups, 4)
	require.ErrorIs(t, results[0].err, context.Canceled)
	require.NoError(t, results[1].err)
	require.Equal(t, ReasonSuccess, results[1].reason)
}

// A plugin may alter content, but identity changes cannot reuse admission facts.
func TestSendBanReauthorizesPluginIdentityMutation(t *testing.T) {
	base := newFakePermissionStore()
	base.userPolicies["banned"] = metadb.SendBanResult{SendBan: 1}
	store := &recordingPermissionBatchStore{base: base}
	hook := &recordingSendHook{mutate: func(cmd SendCommand) (SendCommand, Reason, error) {
		cmd.FromUID = "banned"
		return cmd, ReasonSuccess, nil
	}}
	submitter := &recordingSubmitter{}
	app := New(Options{PermissionStore: store, PermissionBatchStore: store, SendHook: hook, Submitter: submitter})
	out, err := app.Send(context.Background(), SendCommand{FromUID: "allowed", ChannelID: "info", ChannelType: channelTypeInfo, Payload: []byte("x")})
	require.NoError(t, err)
	require.Equal(t, ReasonSendBan, out.Reason)
	require.Empty(t, submitter.sendCommand.FromUID)
}

// Gateway assigns one batch deadline but the usecase derives independent child
// contexts; cancellation isolation must not turn that batch into per-item RPCs.
func TestSendBanGatewayDeadlineBatchStillCoalesces(t *testing.T) {
	base := newFakePermissionStore()
	store := &recordingPermissionBatchStore{base: base}
	deadline := time.Now().Add(time.Minute)
	parent, stop := context.WithCancel(context.Background())
	defer stop()
	items := make([]SendBatchItem, 8)
	for i := range items {
		items[i] = SendBatchItem{Context: parent, Deadline: deadline, Command: SendCommand{FromUID: "u", ChannelID: "info", ChannelType: channelTypeInfo}}
	}
	app := New(Options{PermissionStore: store, PermissionBatchStore: store, Submitter: &recordingSubmitter{}})
	app.SendBatch(items)
	require.EqualValues(t, 1, store.batchCalls.Load())
	require.Len(t, store.reads, 2)
}
