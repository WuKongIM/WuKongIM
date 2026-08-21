package message

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestSendBatchFailsClosedWhenSubmitterReturnsShortResultVector(t *testing.T) {
	submitter := &recordingSubmitter{
		batchResults: []SendBatchItemResult{{
			Result: SendResult{MessageID: 10, MessageSeq: 2, Reason: ReasonSuccess},
		}},
	}
	app := New(Options{Submitter: submitter})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "a", ChannelType: 2, Payload: []byte("one")}},
		{Command: SendCommand{FromUID: "u2", ChannelID: "b", ChannelType: 2, Payload: []byte("two")}},
	}

	results := app.SendBatch(items)

	if len(results) != len(items) {
		t.Fatalf("SendBatch() result count = %d, want %d", len(results), len(items))
	}
	for index, result := range results {
		if !errors.Is(result.Err, ErrSendBatchEmissionMismatch) {
			t.Fatalf("result %d error = %v, want ErrSendBatchEmissionMismatch", index, result.Err)
		}
		if result.Result.Reason != ReasonSystemError {
			t.Fatalf("result %d reason = %v, want system error", index, result.Result.Reason)
		}
	}
}

func TestSendBatchObservesBoundedStages(t *testing.T) {
	observer := &recordingSendBatchStageObserver{}
	app := New(Options{
		Submitter:         &recordingSubmitter{batchResults: []SendBatchItemResult{{Result: SendResult{Reason: ReasonSuccess}}}},
		SendBatchObserver: observer,
	})

	results := app.SendBatch([]SendBatchItem{{Command: SendCommand{
		FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("one"),
	}}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("SendBatch() = %#v, want success", results)
	}
	if got, want := observer.stages(), []string{"permission", "pre_append", "submitter"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("observed stages = %v, want %v", got, want)
	}
	for _, event := range observer.events {
		if event.Result != "ok" || event.Items != 1 || event.Duration <= 0 {
			t.Fatalf("stage observation = %#v, want ok/1/positive duration", event)
		}
	}
}

func TestSendBatchAnnotatesSubmitterTimeoutWithConsumedDeadlineBudget(t *testing.T) {
	deadline := time.Now().Add(100 * time.Millisecond)
	app := New(Options{
		PersonDirectory: delayedPersonDirectoryEnsurer{delay: 20 * time.Millisecond},
		Submitter: &recordingSubmitter{batchResults: []SendBatchItemResult{{
			Err: context.DeadlineExceeded,
		}}},
	})

	results := app.SendBatch([]SendBatchItem{{
		Context: context.Background(), Deadline: deadline,
		Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson},
	}})

	if len(results) != 1 || !errors.Is(results[0].Err, context.DeadlineExceeded) {
		t.Fatalf("SendBatch() = %#v, want wrapped deadline", results)
	}
	diagnostics, ok := SendBatchFailureDiagnosticsFromError(results[0].Err)
	if !ok || diagnostics.FailedStage != sendBatchStageSubmitter ||
		diagnostics.PreAppend < 15*time.Millisecond || diagnostics.DeadlineBudgetBeforeSubmit <= 0 ||
		diagnostics.DeadlineBudgetBeforeSubmit >= 95*time.Millisecond {
		t.Fatalf("failure diagnostics = %#v/%v", diagnostics, ok)
	}
}

func TestSendBatchBoundsConcurrentPermissionChecksAndPreservesOrder(t *testing.T) {
	const itemCount = sendBatchPermissionWorkers * 2
	store := &blockingBatchPermissionStore{
		entered: make(chan struct{}, itemCount),
		release: make(chan struct{}),
	}
	batchResults := make([]SendBatchItemResult, itemCount)
	items := make([]SendBatchItem, itemCount)
	for i := range items {
		batchResults[i] = SendBatchItemResult{Result: SendResult{MessageID: uint64(i + 1), Reason: ReasonSuccess}}
		items[i] = SendBatchItem{Command: SendCommand{
			FromUID: "system", ChannelID: fmt.Sprintf("channel-%02d", i), ChannelType: channelTypeInfo,
		}}
	}
	submitter := &recordingSubmitter{batchResults: batchResults}
	app := New(Options{
		Submitter:       submitter,
		PermissionStore: store,
		SystemUIDs:      fakeSystemUIDChecker{"system": true},
	})

	resultCh := make(chan []SendBatchItemResult, 1)
	go func() {
		resultCh <- app.SendBatch(items)
	}()

	for i := 0; i < sendBatchPermissionWorkers; i++ {
		select {
		case <-store.entered:
		case <-time.After(time.Second):
			t.Fatalf("permission checks entered = %d, want %d concurrent checks", i, sendBatchPermissionWorkers)
		}
	}
	select {
	case <-store.entered:
		t.Fatalf("permission checks exceeded worker bound %d", sendBatchPermissionWorkers)
	case <-time.After(25 * time.Millisecond):
	}
	close(store.release)

	var results []SendBatchItemResult
	select {
	case results = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("SendBatch did not finish after permission checks were released")
	}
	if got := store.peak.Load(); got != sendBatchPermissionWorkers {
		t.Fatalf("peak permission checks = %d, want %d", got, sendBatchPermissionWorkers)
	}
	if !reflect.DeepEqual(results, batchResults) {
		t.Fatalf("SendBatch() = %#v, want item-aligned results %#v", results, batchResults)
	}
	if len(submitter.batchItems) != 1 || !reflect.DeepEqual(submitter.batchItems[0], items) {
		t.Fatalf("delegated items = %#v, want original order", submitter.batchItems)
	}
}

func TestSendBatchCoalescesEquivalentPermissionScopes(t *testing.T) {
	const itemCount = 64
	store := newFakePermissionStore()
	store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{
		ChannelID:   "g1",
		ChannelType: int64(channelTypeGroup),
	}
	store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
	batchResults := make([]SendBatchItemResult, itemCount)
	items := make([]SendBatchItem, itemCount)
	for i := range items {
		batchResults[i] = SendBatchItemResult{Result: SendResult{MessageID: uint64(i + 1), Reason: ReasonSuccess}}
		items[i] = SendBatchItem{Command: SendCommand{
			FromUID:     "u1",
			ClientSeq:   uint64(i + 1),
			ClientMsgNo: fmt.Sprintf("message-%02d", i+1),
			ChannelID:   "g1",
			ChannelType: channelTypeGroup,
			Payload:     []byte(fmt.Sprintf("payload-%02d", i+1)),
		}}
	}
	submitter := &recordingSubmitter{batchResults: batchResults}
	app := New(Options{Submitter: submitter, PermissionStore: store})

	results := app.SendBatch(items)

	if !reflect.DeepEqual(results, batchResults) {
		t.Fatalf("SendBatch() = %#v, want item-aligned delegated results", results)
	}
	if got := store.getChannelCalls.Load(); got != 2 {
		t.Fatalf("GetChannelForPermission calls = %d, want sender and group checked once", got)
	}
	if got := store.containsCalls.Load(); got != 2 {
		t.Fatalf("ContainsChannelSubscriber calls = %d, want denylist and membership checked once", got)
	}
	if got := store.hasAnyCalls.Load(); got != 1 {
		t.Fatalf("HasChannelSubscribers calls = %d, want allowlist checked once", got)
	}
	if len(submitter.batchItems) != 1 || !reflect.DeepEqual(submitter.batchItems[0], items) {
		t.Fatalf("delegated items = %#v, want every original command in order", submitter.batchItems)
	}
}

func TestSendBatchUsesOneAuthoritativePermissionReadBatchForDistinctGroups(t *testing.T) {
	const itemCount = 64
	base := newFakePermissionStore()
	items := make([]SendBatchItem, itemCount)
	batchResults := make([]SendBatchItemResult, itemCount)
	for i := range items {
		channelID := fmt.Sprintf("group-%02d", i+1)
		base.channels[permissionKey(channelID, int64(channelTypeGroup))] = metadb.Channel{
			ChannelID:   channelID,
			ChannelType: int64(channelTypeGroup),
		}
		base.members[permissionKey(channelID, int64(channelTypeGroup))] = map[string]bool{"u1": true}
		items[i] = SendBatchItem{Command: SendCommand{
			FromUID:     "u1",
			ChannelID:   channelID,
			ChannelType: channelTypeGroup,
			Payload:     []byte(channelID),
		}}
		batchResults[i] = SendBatchItemResult{Result: SendResult{MessageID: uint64(i + 1), Reason: ReasonSuccess}}
	}
	store := &recordingPermissionBatchStore{base: base}
	submitter := &recordingSubmitter{batchResults: batchResults}
	app := New(Options{Submitter: submitter, PermissionStore: store, PermissionBatchStore: store})

	results := app.SendBatch(items)

	if !reflect.DeepEqual(results, batchResults) {
		t.Fatalf("SendBatch() = %#v, want item-aligned delegated results", results)
	}
	if got := store.batchCalls.Load(); got != 1 {
		t.Fatalf("ReadPermissionsBatch calls = %d, want 1", got)
	}
	if got := base.getChannelCalls.Load() + base.containsCalls.Load() + base.hasAnyCalls.Load(); got != 0 {
		t.Fatalf("point permission calls = %d, want 0 when authoritative batch is available", got)
	}
	if got, want := len(store.reads), 1+itemCount*5; got != want {
		t.Fatalf("batched permission reads = %d, want %d deduplicated facts", got, want)
	}
	if len(submitter.batchItems) != 1 || !reflect.DeepEqual(submitter.batchItems[0], items) {
		t.Fatalf("delegated items = %#v, want every original command in order", submitter.batchItems)
	}
}

func TestSendBatchPermissionReadPlanMatchesSingleSendPolicy(t *testing.T) {
	groupKey := channelmembers.ChannelKey{ChannelID: "g1", ChannelType: channelTypeGroup}
	denyID := channelmembers.DenylistChannelID(groupKey)
	allowID := channelmembers.AllowlistChannelID(groupKey)
	personKey := channelmembers.ChannelKey{ChannelID: "u2", ChannelType: channelTypePerson}
	personDenyID := channelmembers.DenylistChannelID(personKey)
	personAllowID := channelmembers.AllowlistChannelID(personKey)
	tests := []struct {
		name      string
		cmd       SendCommand
		configure func(*fakePermissionStore)
		opts      func(*Options)
		want      Reason
	}{
		{
			name: "allowed member",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
				store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
			},
			want: ReasonSuccess,
		},
		{
			name: "sender send ban precedes group state",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("u1", int64(channelTypePerson))] = metadb.Channel{SendBan: 1}
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{Ban: 1}
			},
			want: ReasonSendBan,
		},
		{
			name: "missing group",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			want: ReasonChannelNotExist,
		},
		{
			name: "banned group",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{Ban: 1}
			},
			want: ReasonBan,
		},
		{
			name: "disbanded group",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{Disband: 1}
			},
			want: ReasonDisband,
		},
		{
			name: "denylist precedes subscriber",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{}
				store.members[permissionKey(denyID, int64(channelTypeGroup))] = map[string]bool{"u1": true}
			},
			want: ReasonInBlacklist,
		},
		{
			name: "missing subscriber",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{}
			},
			want: ReasonSubscriberNotExist,
		},
		{
			name: "nonempty allowlist miss",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{}
				store.members[permissionKey("g1", int64(channelTypeGroup))] = map[string]bool{"u1": true}
				store.hasAny[permissionKey(allowID, int64(channelTypeGroup))] = true
			},
			want: ReasonNotInWhitelist,
		},
		{
			name: "system uid may target missing group",
			cmd:  SendCommand{FromUID: "sys", ChannelID: "g1", ChannelType: channelTypeGroup},
			opts: func(opts *Options) {
				opts.SystemUIDs = fakeSystemUIDChecker{"sys": true}
			},
			want: ReasonSuccess,
		},
		{
			name: "system uid still rejects disband",
			cmd:  SendCommand{FromUID: "sys", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{Disband: 1}
			},
			opts: func(opts *Options) {
				opts.SystemUIDs = fakeSystemUIDChecker{"sys": true}
			},
			want: ReasonDisband,
		},
		{
			name: "system device bypass follows sender send ban",
			cmd:  SendCommand{FromUID: "u1", DeviceID: "system-device", ChannelID: "g1", ChannelType: channelTypeGroup},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{}
			},
			opts: func(opts *Options) {
				opts.SystemDeviceID = "system-device"
			},
			want: ReasonSuccess,
		},
		{
			name: "ordinary person send",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, NormalizePersonChannel: true},
			want: ReasonSuccess,
		},
		{
			name: "person sender send ban precedes terminal state",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, NormalizePersonChannel: true},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("u1", int64(channelTypePerson))] = metadb.Channel{SendBan: 1}
				store.channels[permissionKey("u1@u2", int64(channelTypePerson))] = metadb.Channel{Disband: 1}
			},
			want: ReasonSendBan,
		},
		{
			name: "person denylist",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, NormalizePersonChannel: true},
			configure: func(store *fakePermissionStore) {
				store.members[permissionKey(personDenyID, int64(channelTypePerson))] = map[string]bool{"u1": true}
			},
			want: ReasonInBlacklist,
		},
		{
			name: "person whitelist member",
			cmd:  SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, NormalizePersonChannel: true},
			configure: func(store *fakePermissionStore) {
				store.members[permissionKey(personAllowID, int64(channelTypePerson))] = map[string]bool{"u1": true}
			},
			opts: func(opts *Options) {
				opts.PersonWhitelistEnabled = true
			},
			want: ReasonSuccess,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			singleStore := newFakePermissionStore()
			batchBase := newFakePermissionStore()
			if tc.configure != nil {
				tc.configure(singleStore)
				tc.configure(batchBase)
			}
			singleSubmitter := &recordingSubmitter{sendResult: SendResult{MessageID: 1, Reason: ReasonSuccess}}
			singleOpts := Options{Submitter: singleSubmitter, PermissionStore: singleStore}
			batchSubmitter := &recordingSubmitter{batchResults: []SendBatchItemResult{{Result: SendResult{MessageID: 1, Reason: ReasonSuccess}}}}
			batchStore := &recordingPermissionBatchStore{base: batchBase}
			batchOpts := Options{Submitter: batchSubmitter, PermissionStore: batchStore, PermissionBatchStore: batchStore}
			if tc.opts != nil {
				tc.opts(&singleOpts)
				tc.opts(&batchOpts)
			}

			singleResult, singleErr := New(singleOpts).Send(context.Background(), tc.cmd)
			batchResults := New(batchOpts).SendBatch([]SendBatchItem{{Context: context.Background(), Command: tc.cmd}})
			if len(batchResults) != 1 {
				t.Fatalf("batch results len = %d, want 1", len(batchResults))
			}
			if singleErr != nil || batchResults[0].Err != nil {
				t.Fatalf("single/batch errors = %v/%v, want nil", singleErr, batchResults[0].Err)
			}
			if singleResult.Reason != tc.want || batchResults[0].Result.Reason != tc.want {
				t.Fatalf("single/batch reasons = %v/%v, want %v", singleResult.Reason, batchResults[0].Result.Reason, tc.want)
			}
			if got := batchStore.batchCalls.Load(); got != 1 {
				t.Fatalf("permission batch calls = %d, want 1", got)
			}
		})
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

func TestSendEnsuresPersistentPersonDirectoryBeforeSubmit(t *testing.T) {
	ensureErr := errors.New("directory unavailable")
	ensurer := &recordingPersonDirectoryEnsurer{err: ensureErr}
	submitter := &recordingSubmitter{sendResult: SendResult{MessageID: 1, Reason: ReasonSuccess}}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})

	cmd := SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, NormalizePersonChannel: true, Payload: []byte("hi")}
	if _, err := app.Send(context.Background(), cmd); !errors.Is(err, ensureErr) {
		t.Fatalf("Send() error = %v, want %v", err, ensureErr)
	}
	canonical, err := runtimechannelid.NormalizePersonChannel("u1", "u2")
	if err != nil {
		t.Fatalf("NormalizePersonChannel(): %v", err)
	}
	if !reflect.DeepEqual(ensurer.channelIDs, []string{canonical}) || submitter.sendCommand.FromUID != "" {
		t.Fatalf("ensurer=%+v submitter=%+v", ensurer.channelIDs, submitter.sendCommand)
	}

	ensurer.err = nil
	cmd.SyncOnce = true
	if _, err := app.Send(context.Background(), cmd); err != nil {
		t.Fatalf("Send(sync once): %v", err)
	}
	if len(ensurer.channelIDs) != 1 {
		t.Fatalf("ensurer calls = %+v, want persistent ordinary only", ensurer.channelIDs)
	}
}

func TestSendBatchCarriesAuthoritativePersonChannelFactIntoDirectoryAdmission(t *testing.T) {
	base := newFakePermissionStore()
	permissions := &recordingPermissionBatchStore{base: base}
	directories := &recordingPersonDirectoryEnsurer{}
	app := New(Options{
		PermissionStore: permissions, PermissionBatchStore: permissions,
		PersonDirectory: directories,
		Submitter:       &recordingSubmitter{batchResults: []SendBatchItemResult{{Result: SendResult{Reason: ReasonSuccess}}}},
	})

	results := app.SendBatch([]SendBatchItem{{Command: SendCommand{
		FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson,
	}}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("SendBatch() = %#v, want success", results)
	}
	directories.mu.Lock()
	defer directories.mu.Unlock()
	if len(directories.admissions) != 1 || directories.admissions[0].ChannelFact == nil {
		t.Fatalf("directory admissions = %#v, want one authoritative channel fact", directories.admissions)
	}
	if directories.admissions[0].ChannelFact.Found {
		t.Fatalf("directory channel fact = %#v, want authoritative missing fact", directories.admissions[0].ChannelFact)
	}
}

func TestSendBatchAdmitsDistinctPersonDirectoriesInOneBoundedWave(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	ensurer := &blockingPersonDirectoryEnsurer{
		started: make(chan string, 3), release: release, calls: make(map[string]int),
	}
	submitter := &recordingSubmitter{batchResults: []SendBatchItemResult{
		{Result: SendResult{Reason: ReasonSuccess}},
		{Result: SendResult{Reason: ReasonSuccess}},
		{Result: SendResult{Reason: ReasonSuccess}},
	}}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u2", ChannelID: "u1@u2", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u3", ChannelID: "u3@u4", ChannelType: channelTypePerson}},
	}

	done := make(chan []SendBatchItemResult, 1)
	go func() { done <- app.SendBatch(items) }()
	started := make(map[string]struct{}, 2)
	for len(started) < 2 {
		select {
		case channelID := <-ensurer.started:
			started[channelID] = struct{}{}
		case <-time.After(time.Second):
			close(release)
			t.Fatal("distinct directory establishment remained serial")
		}
	}
	close(release)
	results := <-done

	if len(results) != len(items) {
		t.Fatalf("SendBatch() len = %d, want %d", len(results), len(items))
	}
	if ensurer.batchCallCount() != 1 {
		t.Fatalf("person directory batch calls = %d, want 1", ensurer.batchCallCount())
	}
	if calls := ensurer.callCounts(); !reflect.DeepEqual(calls, map[string]int{"u1@u2": 1, "u3@u4": 1}) {
		t.Fatalf("directory calls = %#v, want one per distinct channel", calls)
	}
}

func TestSendBatchDoesNotHoldReadyPersonChannelsBehindColdDirectorySetup(t *testing.T) {
	t.Parallel()

	releaseCold := make(chan struct{})
	ensurer := &mixedReadinessPersonDirectoryEnsurer{
		ready:       map[string]bool{"u3@u4": true},
		coldStarted: make(chan struct{}),
		releaseCold: releaseCold,
	}
	submitter := &signalingBatchSubmitter{submitted: make(chan string, 2)}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u3", ChannelID: "u3@u4", ChannelType: channelTypePerson}},
	}

	done := make(chan []SendBatchItemResult, 1)
	go func() { done <- app.SendBatch(items) }()
	select {
	case <-ensurer.coldStarted:
	case <-time.After(time.Second):
		close(releaseCold)
		t.Fatal("cold directory setup did not start")
	}
	select {
	case channelID := <-submitter.submitted:
		if channelID != "u3@u4" {
			close(releaseCold)
			t.Fatalf("first submitted channel = %q, want ready channel", channelID)
		}
	case <-time.After(time.Second):
		close(releaseCold)
		t.Fatal("ready person channel remained blocked behind cold directory setup")
	}
	close(releaseCold)
	results := <-done
	if len(results) != len(items) || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("SendBatch() = %#v, want two aligned successes", results)
	}
	select {
	case channelID := <-submitter.submitted:
		if channelID != "u1@u2" {
			t.Fatalf("second submitted channel = %q, want cold channel after setup", channelID)
		}
	default:
		t.Fatal("cold channel was not submitted after directory setup")
	}
}

func TestSendBatchEachEmitsReadyPersonResultBeforeColdDirectoryCompletes(t *testing.T) {
	t.Parallel()

	releaseCold := make(chan struct{})
	ensurer := &mixedReadinessPersonDirectoryEnsurer{
		ready:       map[string]bool{"u3@u4": true},
		coldStarted: make(chan struct{}),
		releaseCold: releaseCold,
	}
	submitter := &signalingBatchSubmitter{submitted: make(chan string, 2)}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u3", ChannelID: "u3@u4", ChannelType: channelTypePerson}},
	}
	emitted := make(chan int, len(items))
	done := make(chan error, 1)
	go func() {
		done <- app.SendBatchEach(items, func(index int, result SendBatchItemResult) error {
			if result.Err != nil || result.Result.Reason != ReasonSuccess {
				return fmt.Errorf("item %d result: %#v", index, result)
			}
			emitted <- index
			return nil
		})
	}()
	select {
	case <-ensurer.coldStarted:
	case <-time.After(time.Second):
		close(releaseCold)
		t.Fatal("cold directory setup did not start")
	}
	select {
	case index := <-emitted:
		if index != 1 {
			close(releaseCold)
			t.Fatalf("first emitted index = %d, want ready index 1", index)
		}
	case <-time.After(time.Second):
		close(releaseCold)
		t.Fatal("ready result remained blocked behind cold directory completion")
	}
	select {
	case err := <-done:
		close(releaseCold)
		t.Fatalf("SendBatchEach returned before cold result completed: %v", err)
	default:
	}
	close(releaseCold)
	select {
	case index := <-emitted:
		if index != 0 {
			t.Fatalf("second emitted index = %d, want cold index 0", index)
		}
	case <-time.After(time.Second):
		t.Fatal("cold result was not emitted after directory completion")
	}
	if err := <-done; err != nil {
		t.Fatalf("SendBatchEach() error = %v", err)
	}
}

func TestSendBatchEachEmitsReadySubmitterResultBeforeSlowChannelCompletes(t *testing.T) {
	t.Parallel()

	releaseSlow := make(chan struct{})
	submitter := &streamingBatchSubmitter{
		started:     make(chan struct{}),
		releaseSlow: releaseSlow,
	}
	app := New(Options{Submitter: submitter})
	items := []SendBatchItem{
		{Context: context.Background(), Command: SendCommand{FromUID: "u1", ChannelID: "c1", ChannelType: channelTypeGroup}},
		{Context: context.Background(), Command: SendCommand{FromUID: "u2", ChannelID: "c2", ChannelType: channelTypeGroup}},
	}
	emitted := make(chan int, len(items))
	done := make(chan error, 1)
	go func() {
		done <- app.SendBatchEach(items, func(index int, result SendBatchItemResult) error {
			if result.Err != nil || result.Result.Reason != ReasonSuccess {
				return fmt.Errorf("item %d result: %#v", index, result)
			}
			emitted <- index
			return nil
		})
	}()
	select {
	case <-submitter.started:
	case <-time.After(time.Second):
		close(releaseSlow)
		t.Fatal("batch submitter did not start")
	}
	select {
	case index := <-emitted:
		if index != 0 {
			close(releaseSlow)
			t.Fatalf("first emitted index = %d, want ready index 0", index)
		}
	case <-time.After(100 * time.Millisecond):
		close(releaseSlow)
		<-done
		t.Fatal("ready channel result remained blocked behind slow channel completion")
	}
	select {
	case err := <-done:
		close(releaseSlow)
		t.Fatalf("SendBatchEach returned before slow channel completed: %v", err)
	default:
	}
	close(releaseSlow)
	select {
	case index := <-emitted:
		if index != 1 {
			t.Fatalf("second emitted index = %d, want slow index 1", index)
		}
	case <-time.After(time.Second):
		t.Fatal("slow channel result was not emitted after completion")
	}
	if err := <-done; err != nil {
		t.Fatalf("SendBatchEach() error = %v", err)
	}
}

func TestSendBatchDoesNotHoldAuthoritativelyReadyPersonChannelBehindColdSetup(t *testing.T) {
	t.Parallel()

	releaseCold := make(chan struct{})
	ensurer := &mixedLatencyPersonDirectoryEnsurer{
		coldChannelID: "u1@u2",
		coldStarted:   make(chan struct{}),
		releaseCold:   releaseCold,
	}
	submitter := &signalingBatchSubmitter{submitted: make(chan string, 2)}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u3", ChannelID: "u3@u4", ChannelType: channelTypePerson}},
	}

	done := make(chan []SendBatchItemResult, 1)
	go func() { done <- app.SendBatch(items) }()
	select {
	case <-ensurer.coldStarted:
	case <-time.After(time.Second):
		close(releaseCold)
		t.Fatal("cold directory setup did not start")
	}
	select {
	case channelID := <-submitter.submitted:
		if channelID != "u3@u4" {
			close(releaseCold)
			t.Fatalf("first submitted channel = %q, want independently resolved channel", channelID)
		}
	case <-time.After(time.Second):
		close(releaseCold)
		t.Fatal("authoritatively ready person channel remained blocked behind cold setup")
	}
	close(releaseCold)
	results := <-done
	if len(results) != len(items) || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("SendBatch() = %#v, want two aligned successes", results)
	}
}

func TestSendBatchStreamsCompletedPersonDirectoryWavesToSubmitter(t *testing.T) {
	t.Parallel()

	releaseSlow := make(chan struct{})
	ensurer := &stagedWavePersonDirectoryEnsurer{releaseSlow: releaseSlow}
	submitter := &signalingBatchSubmitter{submitted: make(chan string, 2)}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u3", ChannelID: "u3@u4", ChannelType: channelTypePerson}},
	}

	done := make(chan []SendBatchItemResult, 1)
	go func() { done <- app.SendBatch(items) }()
	select {
	case channelID := <-submitter.submitted:
		if channelID != "u3@u4" {
			close(releaseSlow)
			t.Fatalf("first submitted channel = %q, want completed directory wave u3@u4", channelID)
		}
	case <-time.After(time.Second):
		close(releaseSlow)
		t.Fatal("completed directory wave remained blocked behind slow sibling")
	}
	select {
	case results := <-done:
		close(releaseSlow)
		t.Fatalf("SendBatch returned before all admitted directory work joined: %#v", results)
	default:
	}
	close(releaseSlow)
	select {
	case channelID := <-submitter.submitted:
		if channelID != "u1@u2" {
			t.Fatalf("second submitted channel = %q, want released directory wave u1@u2", channelID)
		}
	case <-time.After(time.Second):
		t.Fatal("slow directory wave was not submitted after release")
	}
	results := <-done
	if len(results) != len(items) || results[0].Err != nil || results[1].Err != nil {
		t.Fatalf("SendBatch() = %#v, want two aligned successes", results)
	}
}

func TestSendBatchPreservesSameSessionAdmissionOrderAcrossDirectoryWaves(t *testing.T) {
	t.Parallel()

	releaseSlow := make(chan struct{})
	ensurer := &stagedWavePersonDirectoryEnsurer{releaseSlow: releaseSlow}
	submitter := &signalingBatchSubmitter{submitted: make(chan string, 3)}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson, SenderNodeID: 9, SenderSessionID: 7}},
		{Command: SendCommand{FromUID: "u3", ChannelID: "u3@u4", ChannelType: channelTypePerson, SenderNodeID: 9, SenderSessionID: 7}},
		{Command: SendCommand{FromUID: "u5", ChannelID: "u5@u6", ChannelType: channelTypePerson, SenderNodeID: 9, SenderSessionID: 8}},
	}

	done := make(chan []SendBatchItemResult, 1)
	go func() { done <- app.SendBatch(items) }()
	select {
	case got := <-submitter.submitted:
		if got != "u5@u6" {
			close(releaseSlow)
			t.Fatalf("first submitted channel = %q, want independent session u5@u6", got)
		}
	case <-time.After(time.Second):
		close(releaseSlow)
		t.Fatal("independent session was blocked behind cold directory")
	}
	select {
	case got := <-submitter.submitted:
		close(releaseSlow)
		t.Fatalf("same-session later channel %q submitted before earlier cold channel", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseSlow)
	if results := <-done; len(results) != len(items) {
		t.Fatalf("SendBatch() results = %d, want %d", len(results), len(items))
	}
	if got := <-submitter.submitted; got != "u1@u2" {
		t.Fatalf("second submitted channel = %q, want earlier same-session u1@u2", got)
	}
	if got := <-submitter.submitted; got != "u3@u4" {
		t.Fatalf("third submitted channel = %q, want later same-session u3@u4", got)
	}
}

func TestSendBatchFillsOnePersonDirectoryBatchWithoutCollectDelay(t *testing.T) {
	t.Parallel()

	const directoryBatchItems = 128
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	ensurer := &blockingPersonDirectoryEnsurer{
		started: make(chan string, directoryBatchItems), release: release, calls: make(map[string]int),
	}
	results := make([]SendBatchItemResult, directoryBatchItems)
	items := make([]SendBatchItem, directoryBatchItems)
	for i := range items {
		channelID := fmt.Sprintf("u%04d@z", i)
		items[i] = SendBatchItem{Command: SendCommand{FromUID: "z", ChannelID: channelID, ChannelType: channelTypePerson}}
		results[i] = SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}}
	}
	app := New(Options{
		Submitter:       &recordingSubmitter{batchResults: results},
		PersonDirectory: ensurer,
	})

	done := make(chan []SendBatchItemResult, 1)
	go func() { done <- app.SendBatch(items) }()
	for started := 0; started < directoryBatchItems; started++ {
		select {
		case <-ensurer.started:
		case <-time.After(time.Second):
			t.Fatalf("person directory calls started = %d, want %d before the downstream collect deadline", started, directoryBatchItems)
		}
	}
	releaseAll()
	if got := <-done; len(got) != directoryBatchItems {
		t.Fatalf("SendBatch() len = %d, want %d", len(got), directoryBatchItems)
	}
}

func TestSendBatchPersonDirectoryFailureRemainsItemAligned(t *testing.T) {
	t.Parallel()

	directoryErr := errors.New("directory unavailable")
	ensurer := &recordingPersonDirectoryEnsurer{errs: map[string]error{"u3@u4": directoryErr}}
	submitter := &recordingSubmitter{batchResults: []SendBatchItemResult{{
		Result: SendResult{MessageID: 41, Reason: ReasonSuccess},
	}}}
	app := New(Options{Submitter: submitter, PersonDirectory: ensurer})
	items := []SendBatchItem{
		{Command: SendCommand{FromUID: "u1", ChannelID: "u1@u2", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u3", ChannelID: "u3@u4", ChannelType: channelTypePerson}},
		{Command: SendCommand{FromUID: "u4", ChannelID: "u3@u4", ChannelType: channelTypePerson}},
	}

	results := app.SendBatch(items)

	if len(results) != len(items) || results[0].Result.MessageID != 41 {
		t.Fatalf("SendBatch() = %#v, want first delegated success", results)
	}
	for _, index := range []int{1, 2} {
		if !errors.Is(results[index].Err, directoryErr) || results[index].Result.Reason != ReasonSystemError {
			t.Fatalf("result %d = %#v, want aligned directory error", index, results[index])
		}
	}
	if len(submitter.batchItems) != 1 || len(submitter.batchItems[0]) != 1 || submitter.batchItems[0][0].Command.ChannelID != "u1@u2" {
		t.Fatalf("delegated batch = %#v, want only ready channel", submitter.batchItems)
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
			name: "disbanded group rejects system uid bypass",
			cmd:  SendCommand{FromUID: "sys", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Disband: 1}
			},
			opts: func(opts *Options) {
				opts.SystemUIDs = fakeSystemUIDChecker{"sys": true}
			},
			want: ReasonDisband,
		},
		{
			name: "disbanded command channel rejects system device bypass",
			cmd: SendCommand{
				FromUID: "u1", DeviceID: "____device", ChannelID: runtimechannelid.ToCommandChannel("g1"),
				ChannelType: channelTypeGroup, Payload: []byte("hi"),
			},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Disband: 1}
			},
			opts: func(opts *Options) {
				opts.SystemDeviceID = "____device"
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

func TestSendRejectsTerminalDisbandForEveryChannelType(t *testing.T) {
	personChannelID, err := runtimechannelid.NormalizePersonChannel("u1", "u2")
	if err != nil {
		t.Fatalf("NormalizePersonChannel(): %v", err)
	}
	tests := []SendCommand{
		{FromUID: "u1", ChannelID: "u2", ChannelType: channelTypePerson, NormalizePersonChannel: true, Payload: []byte("hi")},
		{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
		{FromUID: "u1", ChannelID: "info1", ChannelType: channelTypeInfo, Payload: []byte("hi")},
		{FromUID: "u1", ChannelID: "cs1", ChannelType: channelTypeCustomerService, Payload: []byte("hi")},
		{FromUID: "u1", ChannelID: "u1@agent-a", ChannelType: channelTypeAgent, Payload: []byte("hi")},
		{FromUID: "visitor1", ChannelID: "visitor1", ChannelType: channelTypeVisitors, Payload: []byte("hi")},
		{FromUID: "u1", ChannelID: "other1", ChannelType: 99, Payload: []byte("hi")},
	}
	for _, cmd := range tests {
		t.Run(fmt.Sprintf("channel_type_%d", cmd.ChannelType), func(t *testing.T) {
			sourceID := cmd.ChannelID
			if cmd.ChannelType == channelTypePerson {
				sourceID = personChannelID
			}
			store := newFakePermissionStore()
			store.channels[permissionKey(sourceID, int64(cmd.ChannelType))] = metadb.Channel{
				ChannelID: sourceID, ChannelType: int64(cmd.ChannelType), Disband: 1,
			}
			app := New(Options{Submitter: &recordingSubmitter{}, PermissionStore: store})
			result, err := app.Send(context.Background(), cmd)
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			if result.Reason != ReasonDisband {
				t.Fatalf("Send() reason = %v, want %v", result.Reason, ReasonDisband)
			}
		})
	}
}

func TestSendTerminalCheckBypassesLivePermissionCache(t *testing.T) {
	store := newFakePermissionStore()
	key := permissionKey("g1", int64(channelTypeGroup))
	store.channels[key] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
	store.members[key] = map[string]bool{"u1": true}
	submitter := &recordingSubmitter{sendResult: SendResult{MessageID: 1, MessageSeq: 1, Reason: ReasonSuccess}}
	app := New(Options{Submitter: submitter, PermissionStore: store, PermissionCacheTTL: time.Hour})
	cmd := SendCommand{FromUID: "u1", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")}

	first, err := app.Send(context.Background(), cmd)
	if err != nil || first.Reason != ReasonSuccess {
		t.Fatalf("first Send() = %#v, %v, want success", first, err)
	}
	store.channels[key] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Disband: 1}
	second, err := app.Send(context.Background(), cmd)
	if err != nil {
		t.Fatalf("second Send() error = %v", err)
	}
	if second.Reason != ReasonDisband {
		t.Fatalf("second Send() reason = %v, want %v", second.Reason, ReasonDisband)
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
			name: "system uid bypasses nonterminal permission checks",
			cmd:  SendCommand{FromUID: "sys", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("sys", int64(channelTypePerson))] = metadb.Channel{ChannelID: "sys", ChannelType: int64(channelTypePerson), SendBan: 1}
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup)}
			},
			opts: func(opts *Options) {
				opts.SystemUIDs = fakeSystemUIDChecker{"sys": true}
			},
			wantID: 11,
		},
		{
			name: "system device bypasses nonterminal channel checks after sender send ban passes",
			cmd:  SendCommand{FromUID: "u1", DeviceID: "____device", ChannelID: "g1", ChannelType: channelTypeGroup, Payload: []byte("hi")},
			configure: func(store *fakePermissionStore) {
				store.channels[permissionKey("g1", int64(channelTypeGroup))] = metadb.Channel{ChannelID: "g1", ChannelType: int64(channelTypeGroup), Ban: 1}
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

type recordingPersonDirectoryEnsurer struct {
	mu         sync.Mutex
	channelIDs []string
	admissions []PersonDirectoryAdmission
	err        error
	errs       map[string]error
}

type delayedPersonDirectoryEnsurer struct{ delay time.Duration }

func (e delayedPersonDirectoryEnsurer) AdmitPersonChannelDirectory(ctx context.Context, _ string, _ int64) error {
	timer := time.NewTimer(e.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type blockingPersonDirectoryEnsurer struct {
	mu         sync.Mutex
	started    chan string
	release    <-chan struct{}
	calls      map[string]int
	batchCalls int
	active     atomic.Int64
	maxActive  atomic.Int64
}

type mixedReadinessPersonDirectoryEnsurer struct {
	ready       map[string]bool
	coldStarted chan struct{}
	releaseCold <-chan struct{}
	startedOnce sync.Once
}

type mixedLatencyPersonDirectoryEnsurer struct {
	coldChannelID string
	coldStarted   chan struct{}
	releaseCold   <-chan struct{}
	startedOnce   sync.Once
}

type stagedWavePersonDirectoryEnsurer struct {
	releaseSlow <-chan struct{}
}

func (e *stagedWavePersonDirectoryEnsurer) AdmitPersonChannelDirectory(context.Context, string, int64) error {
	return nil
}

func (e *stagedWavePersonDirectoryEnsurer) AdmitPersonChannelDirectoryWaves(
	admissions []PersonDirectoryAdmission,
	emit func([]PersonDirectoryAdmissionOutcome),
) {
	if len(admissions) < 2 {
		emit([]PersonDirectoryAdmissionOutcome{{Index: -1, Err: ErrSendBatchEmissionMismatch}})
		return
	}
	ready := make([]PersonDirectoryAdmissionOutcome, 0, len(admissions)-1)
	for index := 1; index < len(admissions); index++ {
		ready = append(ready, PersonDirectoryAdmissionOutcome{Index: index})
	}
	emit(ready)
	<-e.releaseSlow
	emit([]PersonDirectoryAdmissionOutcome{{Index: 0}})
}

func (e *mixedLatencyPersonDirectoryEnsurer) AdmitPersonChannelDirectory(ctx context.Context, channelID string, _ int64) error {
	if channelID != e.coldChannelID {
		return nil
	}
	e.startedOnce.Do(func() { close(e.coldStarted) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.releaseCold:
		return nil
	}
}

func (e *mixedLatencyPersonDirectoryEnsurer) AdmitPersonChannelDirectoryWaves(
	admissions []PersonDirectoryAdmission,
	emit func([]PersonDirectoryAdmissionOutcome),
) {
	ready := make([]PersonDirectoryAdmissionOutcome, 0, len(admissions))
	cold := make([]PersonDirectoryAdmissionOutcome, 0, 1)
	for index, admission := range admissions {
		if admission.ChannelID == e.coldChannelID {
			cold = append(cold, PersonDirectoryAdmissionOutcome{Index: index})
			continue
		}
		ready = append(ready, PersonDirectoryAdmissionOutcome{Index: index})
	}
	if len(ready) > 0 {
		emit(ready)
	}
	if len(cold) == 0 {
		return
	}
	e.startedOnce.Do(func() { close(e.coldStarted) })
	<-e.releaseCold
	emit(cold)
}

func (e *mixedReadinessPersonDirectoryEnsurer) AdmitPersonChannelDirectory(ctx context.Context, channelID string, _ int64) error {
	if e.ready[channelID] {
		return nil
	}
	e.startedOnce.Do(func() { close(e.coldStarted) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.releaseCold:
		return nil
	}
}

func (e *mixedReadinessPersonDirectoryEnsurer) AdmitPersonChannelDirectoryWaves(
	admissions []PersonDirectoryAdmission,
	emit func([]PersonDirectoryAdmissionOutcome),
) {
	ready := make([]PersonDirectoryAdmissionOutcome, 0, len(admissions))
	cold := make([]PersonDirectoryAdmissionOutcome, 0, len(admissions))
	for index, admission := range admissions {
		if e.ready[admission.ChannelID] {
			ready = append(ready, PersonDirectoryAdmissionOutcome{Index: index})
		} else {
			cold = append(cold, PersonDirectoryAdmissionOutcome{Index: index})
		}
	}
	if len(ready) > 0 {
		emit(ready)
	}
	if len(cold) == 0 {
		return
	}
	e.startedOnce.Do(func() { close(e.coldStarted) })
	<-e.releaseCold
	emit(cold)
}

type signalingBatchSubmitter struct {
	submitted chan string
}

type streamingBatchSubmitter struct {
	startedOnce sync.Once
	started     chan struct{}
	releaseSlow chan struct{}
}

func (s *streamingBatchSubmitter) Send(context.Context, SendCommand) (SendResult, error) {
	return SendResult{}, nil
}

func (s *streamingBatchSubmitter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	s.startedOnce.Do(func() { close(s.started) })
	<-s.releaseSlow
	results := make([]SendBatchItemResult, len(items))
	for i := range results {
		results[i].Result.Reason = ReasonSuccess
	}
	return results
}

func (s *streamingBatchSubmitter) SendBatchEach(items []SendBatchItem, emit func(int, SendBatchItemResult)) {
	s.startedOnce.Do(func() { close(s.started) })
	if len(items) > 0 {
		emit(0, SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}})
	}
	<-s.releaseSlow
	for i := 1; i < len(items); i++ {
		emit(i, SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}})
	}
}

func (s *signalingBatchSubmitter) Send(context.Context, SendCommand) (SendResult, error) {
	return SendResult{}, nil
}

func (s *signalingBatchSubmitter) SendBatch(items []SendBatchItem) []SendBatchItemResult {
	results := make([]SendBatchItemResult, len(items))
	for i := range items {
		s.submitted <- items[i].Command.ChannelID
		results[i] = SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess}}
	}
	return results
}

type recordingSendBatchStageObserver struct {
	events []SendBatchStageObservation
}

func (o *recordingSendBatchStageObserver) ObserveMessageSendBatchStage(event SendBatchStageObservation) {
	o.events = append(o.events, event)
}

func (o *recordingSendBatchStageObserver) stages() []string {
	stages := make([]string, len(o.events))
	for i, event := range o.events {
		stages[i] = event.Stage
	}
	return stages
}

func (e *recordingPersonDirectoryEnsurer) AdmitPersonChannelDirectory(_ context.Context, channelID string, _ int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.channelIDs = append(e.channelIDs, channelID)
	if err := e.errs[channelID]; err != nil {
		return err
	}
	return e.err
}

func (e *recordingPersonDirectoryEnsurer) AdmitPersonChannelDirectories(admissions []PersonDirectoryAdmission) []error {
	e.mu.Lock()
	e.admissions = append(e.admissions, admissions...)
	e.mu.Unlock()
	results := make([]error, len(admissions))
	for i, admission := range admissions {
		results[i] = e.AdmitPersonChannelDirectory(admission.Context, admission.ChannelID, admission.ChannelType)
	}
	return results
}

func (e delayedPersonDirectoryEnsurer) AdmitPersonChannelDirectories(admissions []PersonDirectoryAdmission) []error {
	results := make([]error, len(admissions))
	for i, admission := range admissions {
		results[i] = e.AdmitPersonChannelDirectory(admission.Context, admission.ChannelID, admission.ChannelType)
	}
	return results
}

func (e *blockingPersonDirectoryEnsurer) AdmitPersonChannelDirectory(ctx context.Context, channelID string, _ int64) error {
	e.mu.Lock()
	e.calls[channelID]++
	e.mu.Unlock()
	active := e.active.Add(1)
	defer e.active.Add(-1)
	for {
		peak := e.maxActive.Load()
		if active <= peak || e.maxActive.CompareAndSwap(peak, active) {
			break
		}
	}
	e.started <- channelID
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.release:
		return nil
	}
}

func (e *blockingPersonDirectoryEnsurer) AdmitPersonChannelDirectories(admissions []PersonDirectoryAdmission) []error {
	e.mu.Lock()
	e.batchCalls++
	for _, admission := range admissions {
		e.calls[admission.ChannelID]++
	}
	e.mu.Unlock()
	for _, admission := range admissions {
		e.started <- admission.ChannelID
	}
	<-e.release
	results := make([]error, len(admissions))
	for i, admission := range admissions {
		if admission.Context != nil {
			results[i] = admission.Context.Err()
		}
	}
	return results
}

func (e *mixedReadinessPersonDirectoryEnsurer) AdmitPersonChannelDirectories(admissions []PersonDirectoryAdmission) []error {
	results := make([]error, len(admissions))
	for i, admission := range admissions {
		results[i] = e.AdmitPersonChannelDirectory(admission.Context, admission.ChannelID, admission.ChannelType)
	}
	return results
}

func (e *mixedLatencyPersonDirectoryEnsurer) AdmitPersonChannelDirectories(admissions []PersonDirectoryAdmission) []error {
	results := make([]error, len(admissions))
	for i, admission := range admissions {
		results[i] = e.AdmitPersonChannelDirectory(admission.Context, admission.ChannelID, admission.ChannelType)
	}
	return results
}

func (e *blockingPersonDirectoryEnsurer) callCounts() map[string]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make(map[string]int, len(e.calls))
	for channelID, count := range e.calls {
		result[channelID] = count
	}
	return result
}

func (e *blockingPersonDirectoryEnsurer) batchCallCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.batchCalls
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
	getChannelCalls atomic.Int64
	containsCalls   atomic.Int64
	hasAnyCalls     atomic.Int64
}

type recordingPermissionBatchStore struct {
	base       *fakePermissionStore
	batchCalls atomic.Int64
	reads      []PermissionRead
}

func (s *recordingPermissionBatchStore) GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	return s.base.GetChannelForPermission(ctx, channelID, channelType)
}

func (s *recordingPermissionBatchStore) ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	return s.base.ContainsChannelSubscriber(ctx, channelID, channelType, uid)
}

func (s *recordingPermissionBatchStore) HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	return s.base.HasChannelSubscribers(ctx, channelID, channelType)
}

func (s *recordingPermissionBatchStore) ReadPermissionsBatch(_ context.Context, reads []PermissionRead) []PermissionReadResult {
	s.batchCalls.Add(1)
	s.reads = append([]PermissionRead(nil), reads...)
	results := make([]PermissionReadResult, len(reads))
	for i, read := range reads {
		key := permissionKey(read.ChannelID, read.ChannelType)
		switch read.Kind {
		case PermissionReadChannel:
			if err, ok := s.base.channelErrs[key]; ok {
				results[i].Err = err
				continue
			}
			channel, ok := s.base.channels[key]
			results[i].Channel = channel
			results[i].Found = ok
		case PermissionReadSubscriberContains:
			results[i].Value = s.base.members[key][read.UID]
		case PermissionReadSubscriberHasAny:
			results[i].Value = s.base.hasAny[key]
		default:
			results[i].Err = fmt.Errorf("unexpected permission read kind %d", read.Kind)
		}
	}
	return results
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
	s.getChannelCalls.Add(1)
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

type blockingBatchPermissionStore struct {
	entered chan struct{}
	release chan struct{}
	active  atomic.Int64
	peak    atomic.Int64
}

func (s *blockingBatchPermissionStore) GetChannelForPermission(_ context.Context, _ string, _ int64) (metadb.Channel, error) {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		peak := s.peak.Load()
		if active <= peak || s.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	s.entered <- struct{}{}
	<-s.release
	return metadb.Channel{}, metadb.ErrNotFound
}

func (*blockingBatchPermissionStore) ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error) {
	return false, errors.New("unexpected subscriber lookup")
}

func (*blockingBatchPermissionStore) HasChannelSubscribers(context.Context, string, int64) (bool, error) {
	return false, errors.New("unexpected subscriber-set lookup")
}

func (s *fakePermissionStore) ContainsChannelSubscriber(_ context.Context, channelID string, channelType int64, uid string) (bool, error) {
	s.containsCalls.Add(1)
	return s.members[permissionKey(channelID, channelType)][uid], nil
}

func (s *fakePermissionStore) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	s.hasAnyCalls.Add(1)
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
