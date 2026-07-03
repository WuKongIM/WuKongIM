package message

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestSendBatchDelegatesToSubmitter(t *testing.T) {
	submitter := &recordingSubmitter{
		batchResults: []SendBatchItemResult{
			{Result: SendResult{MessageID: 10, MessageSeq: 2, Reason: ReasonSuccess}},
			{Err: ErrChannelBusy},
		},
	}
	app := New(Options{Submitter: submitter})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 2, Payload: []byte("one")}},
		{Command: SendCommand{FromUID: "u2", ChannelID: "b", ChannelType: 2, Payload: []byte("two")}},
	}

	results := app.SendBatch(items)

	if !reflect.DeepEqual(results, submitter.batchResults) {
		t.Fatalf("SendBatch() = %#v, want delegated results %#v", results, submitter.batchResults)
	}
	if len(submitter.batchItems) != 1 || !reflect.DeepEqual(submitter.batchItems[0], items) {
		t.Fatalf("delegated items = %#v, want original item batch", submitter.batchItems)
	}
}

func TestSendDelegatesToSubmitter(t *testing.T) {
	sendErr := errors.New("send failed")
	submitter := &recordingSubmitter{
		sendResult: SendResult{MessageID: 11, MessageSeq: 3, Reason: ReasonSuccess},
		sendErr:    sendErr,
	}
	app := New(Options{Submitter: submitter})
	ctx := context.Background()
	cmd := SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 2, Payload: []byte("one")}

	result, err := app.Send(ctx, cmd)

	if !errors.Is(err, sendErr) {
		t.Fatalf("Send() error = %v, want delegated error", err)
	}
	if result != submitter.sendResult {
		t.Fatalf("Send() result = %#v, want delegated result", result)
	}
	if submitter.sendCtx != ctx || !reflect.DeepEqual(submitter.sendCommand, cmd) {
		t.Fatalf("delegated send = (%v, %#v), want original context and command", submitter.sendCtx, submitter.sendCommand)
	}
}

func TestSendWithoutSubmitterReturnsRouteNotReady(t *testing.T) {
	app := New(Options{})

	_, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 2, Payload: []byte("one")})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("Send() error = %v, want ErrRouteNotReady", err)
	}
	results := app.SendBatch([]SendBatchItem{{Command: SendCommand{FromUID: "u1"}}})
	if len(results) != 1 || !errors.Is(results[0].Err, ErrRouteNotReady) {
		t.Fatalf("SendBatch() = %#v, want item ErrRouteNotReady", results)
	}
}

func TestSendAppliesLegacyPermissionChecksBeforeSubmitter(t *testing.T) {
	tests := []struct {
		name      string
		cmd       SendCommand
		configure func(*fakePermissionStore)
		opts      func(*Options)
		want      Reason
	}{
		{
			name: "sender send ban wins before channel checks",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("u1", int64(channelTypePerson))] = metadb.Channel{ChannelID: "u1", ChannelType: int64(channelTypePerson), SendBan: 1}
			},
			want: ReasonSendBan,
		},
		{
			name: "missing group channel",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			want: ReasonChannelNotExist,
		},
		{
			name: "banned group",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Ban: 1}
			},
			want: ReasonBan,
		},
		{
			name: "disbanded group",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Disband: 1}
				store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
			},
			want: ReasonDisband,
		},
		{
			name: "group denylist",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
				denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: channelTypeGroup})
				store.members[permissionKey(denyID, int64(channelTypeGroup))] = map[string]bool{"u1": true}
			},
			want: ReasonInBlacklist,
		},
		{
			name: "group missing subscriber",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
			},
			want: ReasonSubscriberNotExist,
		},
		{
			name: "group nonempty allowlist miss",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
				store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
				allowID := channelmembers.AllowlistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: channelTypeGroup})
				store.hasAny[permissionKey(allowID, int64(channelTypeGroup))] = true
				store.members[permissionKey(allowID, int64(channelTypeGroup))] = map[string]bool{"u2": true}
			},
			want: ReasonNotInWhitelist,
		},
		{
			name: "person receiver denylist after normalization",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, Payload: []byte("hi"), NormalizePersonChannel: true},
			configure: func(store *fakePermissionStore) {
				denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: channelTypePerson})
				store.members[permissionKey(denyID, int64(channelTypePerson))] = map[string]bool{"u1": true}
			},
			want: ReasonInBlacklist,
		},
		{
			name: "person whitelist enabled with missing receiver metadata",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, Payload: []byte("hi"), NormalizePersonChannel: true},
			opts: func(opts *Options) {
				opts.PersonWhitelistEnabled = true
			},
			want: ReasonNotInWhitelist,
		},
		{
			name: "agent non participant",
			cmd:  SendCommand{FromUID: "u3", ChannelID: "u1@agent-a", ChannelType: channelTypeAgent, Payload: []byte("hi")},
			want: ReasonNotAllowSend,
		},
		{
			name: "visitors nonself uses customer service membership",
			cmd:  SendCommand{FromUID: "agent1", ChannelID: "visitor1", ChannelType: channelTypeVisitors, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.members[permissionKey("visitor1", int64(channelTypeCustomerService))] = map[string]bool{}
			},
			want: ReasonSubscriberNotExist,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			submitter := &recordingSubmitter{
				sendResult: SendResult{MessageID: 1, MessageSeq: 1, Reason: ReasonSuccess},
			}
			store := newFakePermissionStore()
			if tc.configure != nil {
				tc.configure(store)
			}
			opts := Options{Submitter: submitter, PermissionStore: store}
			if tc.opts != nil {
				tc.opts(&opts)
			}
			app := New(opts)

			result, err := app.Send(context.Background(), tc.cmd)

			if err != nil {
				t.Fatalf("Send() error = %v, want nil", err)
			}
			if result.Reason != tc.want {
				t.Fatalf("Send() reason = %v, want %v", result.Reason, tc.want)
			}
			if submitter.sendCommand.FromUID != "" {
				t.Fatalf("submitter was called with %#v, want permission rejection before delegation", submitter.sendCommand)
			}
		})
	}
}

func TestSendAllowsLegacyPermissionPassesAndBypasses(t *testing.T) {
	tests := []struct {
		name      string
		cmd       SendCommand
		configure func(*fakePermissionStore)
		opts      func(*Options)
		wantID    uint64
	}{
		{
			name: "nil permission store delegates",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			opts: func(opts *Options) {
				opts.PermissionStore = nil
			},
			wantID: 10,
		},
		{
			name: "system uid bypasses all permission checks",
			cmd:  SendCommand{FromUID: "sys", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("sys", int64(channelTypePerson))] = metadb.Channel{ChannelID: "sys", ChannelType: int64(channelTypePerson), SendBan: 1}
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Disband: 1}
			},
			opts: func(opts *Options) {
				opts.SystemUIDs = fakeSystemUIDChecker{"sys": true}
			},
			wantID: 11,
		},
		{
			name: "system device bypasses channel checks after sender send ban passes",
			cmd:  SendCommand{FromUID: "u1", DeviceID: "____device", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Disband: 1}
			},
			opts: func(opts *Options) {
				opts.SystemDeviceID = "____device"
			},
			wantID: 12,
		},
		{
			name: "group subscriber with empty allowlist",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
				store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
			},
			wantID: 13,
		},
		{
			name: "person stranger when whitelist disabled",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, Payload: []byte("hi"), NormalizePersonChannel: true},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("u2", int64(channelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(channelTypePerson)}
			},
			wantID: 14,
		},
		{
			name: "person receiver allows stranger when whitelist enabled",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, Payload: []byte("hi"), NormalizePersonChannel: true},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("u2", int64(channelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(channelTypePerson), AllowStranger: 1}
			},
			opts: func(opts *Options) {
				opts.PersonWhitelistEnabled = true
			},
			wantID: 15,
		},
		{
			name:   "info channel",
			cmd:    SendCommand{FromUID: "u1", ChannelID: "info1", ChannelType: channelTypeInfo, Payload: []byte("hi")},
			wantID: 16,
		},
		{
			name:   "customer service channel",
			cmd:    SendCommand{FromUID: "u1", ChannelID: "cs1", ChannelType: channelTypeCustomerService, Payload: []byte("hi")},
			wantID: 17,
		},
		{
			name:   "agent participant",
			cmd:    SendCommand{FromUID: "u1", ChannelID: "u1@agent-a", ChannelType: channelTypeAgent, Payload: []byte("hi")},
			wantID: 18,
		},
		{
			name:   "visitors self sender",
			cmd:    SendCommand{FromUID: "visitor1", ChannelID: "visitor1", ChannelType: channelTypeVisitors, Payload: []byte("hi")},
			wantID: 19,
		},
		{
			name: "visitors nonself customer service member",
			cmd:  SendCommand{FromUID: "agent1", ChannelID: "visitor1", ChannelType: channelTypeVisitors, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.members[permissionKey("visitor1", int64(channelTypeCustomerService))] = map[string]bool{"agent1": true}
			},
			wantID: 20,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			submitter := &recordingSubmitter{
				sendResult: SendResult{MessageID: tc.wantID, MessageSeq: 2, Reason: ReasonSuccess},
			}
			store := newFakePermissionStore()
			if tc.configure != nil {
				tc.configure(store)
			}
			opts := Options{Submitter: submitter, PermissionStore: store}
			if tc.opts != nil {
				tc.opts(&opts)
			}
			app := New(opts)

			result, err := app.Send(context.Background(), tc.cmd)

			if err != nil {
				t.Fatalf("Send() error = %v, want nil", err)
			}
			if result.MessageID != tc.wantID || result.Reason != ReasonSuccess {
				t.Fatalf("Send() result = %#v, want message id %d success", result, tc.wantID)
			}
			if !reflect.DeepEqual(submitter.sendCommand, wantDelegatedCommand(tc.cmd)) {
				t.Fatalf("delegated command = %#v, want %#v", submitter.sendCommand, wantDelegatedCommand(tc.cmd))
			}
		})
	}
}

func TestSendBatchFiltersPermissionRejectedItemsAndDelegatesAllowedItems(t *testing.T) {
	store := newFakePermissionStore()
	store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
	store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
	store.channels[permissionKey("u2", int64(channelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(channelTypePerson), SendBan: 1}
	submitter := &recordingSubmitter{
		batchResults: []SendBatchItemResult{
			{Result: SendResult{MessageID: 21, MessageSeq: 3, Reason: ReasonSuccess}},
		},
	}
	app := New(Options{Submitter: submitter, PermissionStore: store})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("ok")}},
		{Command: SendCommand{FromUID: "u2", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("blocked")}},
	}

	results := app.SendBatch(items)

	if len(results) != 2 {
		t.Fatalf("SendBatch() len = %d, want 2", len(results))
	}
	if results[0].Result.MessageID != 21 || results[0].Result.Reason != ReasonSuccess {
		t.Fatalf("first result = %#v, want delegated success", results[0])
	}
	if results[1].Result.Reason != ReasonSendBan || results[1].Err != nil {
		t.Fatalf("second result = %#v, want send ban rejection", results[1])
	}
	if len(submitter.batchItems) != 1 || len(submitter.batchItems[0]) != 1 || !reflect.DeepEqual(submitter.batchItems[0][0], items[0]) {
		t.Fatalf("delegated batch = %#v, want only first item", submitter.batchItems)
	}
}

func TestSendHookRunsAfterPermissionAndBeforeSubmitter(t *testing.T) {
	store := newFakePermissionStore()
	store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
	store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
	hook := &recordingSendHook{
		mutate: func(cmd SendCommand) (SendCommand, Reason, error) {
			cmd.Payload = []byte("mutated")
			return cmd, ReasonSuccess, nil
		},
	}
	submitter := &recordingSubmitter{sendResult: SendResult{MessageID: 30, Reason: ReasonSuccess}}
	app := New(Options{Submitter: submitter, PermissionStore: store, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("original")})

	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if result.MessageID != 30 || result.Reason != ReasonSuccess {
		t.Fatalf("Send() result = %#v, want delegated success", result)
	}
	if len(hook.calls) != 1 || string(hook.calls[0].Payload) != "original" {
		t.Fatalf("hook calls = %#v, want original payload after permission", hook.calls)
	}
	if string(submitter.sendCommand.Payload) != "mutated" {
		t.Fatalf("submitter payload = %q, want mutated", submitter.sendCommand.Payload)
	}
}

func TestSendHookRejectsBeforeSubmitter(t *testing.T) {
	hook := &recordingSendHook{
		mutate: func(cmd SendCommand) (SendCommand, Reason, error) {
			return cmd, ReasonNotAllowSend, nil
		},
	}
	submitter := &recordingSubmitter{sendResult: SendResult{MessageID: 31, Reason: ReasonSuccess}}
	app := New(Options{Submitter: submitter, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("blocked")})

	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if result.Reason != ReasonNotAllowSend {
		t.Fatalf("Send() reason = %v, want %v", result.Reason, ReasonNotAllowSend)
	}
	if submitter.sendCommand.FromUID != "" {
		t.Fatalf("submitter was called with %#v, want hook rejection before delegation", submitter.sendCommand)
	}
}

func TestSendBatchHookResultsRemainItemAligned(t *testing.T) {
	store := newFakePermissionStore()
	store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
	store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
	store.channels[permissionKey("u2", int64(channelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(channelTypePerson), SendBan: 1}
	hook := &recordingSendHook{
		mutate: func(cmd SendCommand) (SendCommand, Reason, error) {
			if string(cmd.Payload) == "reject" {
				return cmd, ReasonNotAllowSend, nil
			}
			cmd.Payload = []byte("mutated-" + string(cmd.Payload))
			return cmd, ReasonSuccess, nil
		},
	}
	submitter := &recordingSubmitter{batchResults: []SendBatchItemResult{{
		Result: SendResult{MessageID: 41, Reason: ReasonSuccess},
	}}}
	app := New(Options{Submitter: submitter, PermissionStore: store, SendHook: hook})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("ok")}},
		{Command: SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("reject")}},
		{Command: SendCommand{FromUID: "u2", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("sendban")}},
	}

	results := app.SendBatch(items)

	if len(results) != 3 {
		t.Fatalf("SendBatch() len = %d, want 3", len(results))
	}
	if results[0].Result.MessageID != 41 || results[0].Result.Reason != ReasonSuccess {
		t.Fatalf("first result = %#v, want delegated success", results[0])
	}
	if results[1].Result.Reason != ReasonNotAllowSend || results[1].Err != nil {
		t.Fatalf("second result = %#v, want hook rejection", results[1])
	}
	if results[2].Result.Reason != ReasonSendBan || results[2].Err != nil {
		t.Fatalf("third result = %#v, want permission rejection", results[2])
	}
	if len(submitter.batchItems) != 1 || len(submitter.batchItems[0]) != 1 {
		t.Fatalf("delegated batch = %#v, want one accepted item", submitter.batchItems)
	}
	if got := string(submitter.batchItems[0][0].Command.Payload); got != "mutated-ok" {
		t.Fatalf("delegated payload = %q, want mutated-ok", got)
	}
	if len(hook.calls) != 2 {
		t.Fatalf("hook calls = %d, want 2 permission-accepted items", len(hook.calls))
	}
}

func TestSendHookPluginOriginDepthGuard(t *testing.T) {
	hook := &recordingSendHook{}
	submitter := &recordingSubmitter{sendResult: SendResult{MessageID: 50, Reason: ReasonSuccess}}
	app := New(Options{Submitter: submitter, SendHook: hook})

	_, err := app.Send(context.Background(), SendCommand{
		FromUID: "plugin-a", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("loop"),
		Origin: SendOriginPlugin, HookDepth: DefaultPluginSendMaxHookDepth,
	})
	if !errors.Is(err, ErrSendHookDepthExceeded) {
		t.Fatalf("Send() error = %v, want ErrSendHookDepthExceeded", err)
	}
	if len(hook.calls) != 0 || submitter.sendCommand.FromUID != "" {
		t.Fatalf("hook calls = %d submitter = %#v, want neither called", len(hook.calls), submitter.sendCommand)
	}

	_, err = app.Send(context.Background(), SendCommand{
		FromUID: "plugin-a", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("ok"),
		Origin: SendOriginPlugin,
	})
	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if len(hook.calls) != 1 || hook.calls[0].HookDepth != 1 || hook.calls[0].Origin != SendOriginPlugin {
		t.Fatalf("hook calls = %#v, want plugin origin depth 1", hook.calls)
	}
}

func TestSendHookSkipPluginHooksBypassesHook(t *testing.T) {
	hook := &recordingSendHook{
		mutate: func(cmd SendCommand) (SendCommand, Reason, error) {
			cmd.Payload = []byte("unexpected")
			return cmd, ReasonSuccess, nil
		},
	}
	submitter := &recordingSubmitter{sendResult: SendResult{MessageID: 60, Reason: ReasonSuccess}}
	app := New(Options{Submitter: submitter, SendHook: hook})

	result, err := app.Send(context.Background(), SendCommand{
		FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("original"), SkipPluginHooks: true,
	})

	if err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if result.MessageID != 60 {
		t.Fatalf("Send() result = %#v, want delegated success", result)
	}
	if len(hook.calls) != 0 {
		t.Fatalf("hook calls = %d, want bypassed", len(hook.calls))
	}
	if string(submitter.sendCommand.Payload) != "original" {
		t.Fatalf("submitter payload = %q, want original", submitter.sendCommand.Payload)
	}
}

type recordingSubmitter struct {
	sendCtx      context.Context
	sendCommand  SendCommand
	sendResult   SendResult
	sendErr      error
	batchItems   [][]SendBatchItem
	batchResults []SendBatchItemResult
}

func (s *recordingSubmitter) Send(ctx context.Context, cmd SendCommand) (SendResult, error) {
	s.sendCtx = ctx
	s.sendCommand = cmd
	return s.sendResult, s.sendErr
}

func (s *recordingSubmitter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	s.batchItems = append(s.batchItems, append([]SendBatchItem(nil), items...))
	return append([]SendBatchItemResult(nil), s.batchResults...)
}

func wantDelegatedCommand(cmd SendCommand) SendCommand {
	if cmd.NormalizePersonChannel && cmd.ChannelType == channelTypePerson {
		channelID, err := runtimechannelid.NormalizePersonChannel(cmd.FromUID, cmd.ChannelID)
		if err == nil {
			cmd.ChannelID = channelID
		}
	}
	return cmd
}

type fakePermissionStore struct {
	channels        map[string]metadb.Channel
	channelErrs     map[string]error
	members         map[string]map[string]bool
	hasAny          map[string]bool
	getChannelCalls int
}

func newFakePermissionStore() *fakePermissionStore {
	return &fakePermissionStore{
		channels:    make(map[string]metadb.Channel),
		channelErrs: make(map[string]error),
		members:     make(map[string]map[string]bool),
		hasAny:      make(map[string]bool),
	}
}

func permissionKey(channelID string, channelType int64) string {
	return channelID + "#" + strconv.FormatInt(channelType, 10)
}

func (s *fakePermissionStore) GetChannelForPermission(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	s.getChannelCalls++
	key := permissionKey(channelID, channelType)
	if err, ok := s.channelErrs[key]; ok {
		return metadb.Channel{}, err
	}
	ch, ok := s.channels[key]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return ch, nil
}

func (s *fakePermissionStore) ContainsChannelSubscriber(_ context.Context, channelID string, channelType int64, uid string) (bool, error) {
	return s.members[permissionKey(channelID, channelType)][uid], nil
}

func (s *fakePermissionStore) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	return s.hasAny[permissionKey(channelID, channelType)], nil
}

type fakeSystemUIDChecker map[string]bool

func (f fakeSystemUIDChecker) IsSystemUID(uid string) bool { return f[uid] }

type recordingSendHook struct {
	calls  []SendCommand
	mutate func(SendCommand) (SendCommand, Reason, error)
}

func (h *recordingSendHook) BeforeSend(_ context.Context, cmd SendCommand) (SendCommand, Reason, error) {
	h.calls = append(h.calls, cmd)
	if h.mutate != nil {
		return h.mutate(cmd)
	}
	return cmd, ReasonSuccess, nil
}
