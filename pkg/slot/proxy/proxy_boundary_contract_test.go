package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func TestAuthoritativeRPCRedirectsToAdvertisedLeaderOutsideStalePeerSet(t *testing.T) {
	var calls []multiraft.NodeID
	cluster := &proxyTestMigrationCluster{
		localNodeID: 1,
		leaders:     map[multiraft.SlotID]multiraft.NodeID{7: 2},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{7: {1, 2}},
	}
	cluster.rpcService = func(_ context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
		calls = append(calls, nodeID)
		if slotID != 7 || serviceID != channelRPCServiceID {
			t.Fatalf("unexpected route: slot=%d service=%d", slotID, serviceID)
		}
		req, err := decodeChannelRPCRequest(payload)
		if err != nil {
			t.Fatalf("decode routed request: %v", err)
		}
		if req.SlotID != 7 || req.ChannelID != "channel-a" {
			t.Fatalf("routed request = %+v", req)
		}
		switch nodeID {
		case 2:
			return encodeChannelRPCResponse(channelRPCResponse{Status: rpcStatusNotLeader, LeaderID: 3})
		case 3:
			return encodeChannelRPCResponse(channelRPCResponse{
				Status:  rpcStatusOK,
				Channel: &metadb.Channel{ChannelID: "channel-a", ChannelType: 2},
			})
		default:
			return nil, fmt.Errorf("unexpected peer %d", nodeID)
		}
	}

	store := &Store{cluster: cluster}
	resp, err := store.callChannelRPC(context.Background(), 7, channelRPCRequest{
		Op: channelRPCGetForPermission, SlotID: 7, ChannelID: "channel-a", ChannelType: 2,
	})
	if err != nil {
		t.Fatalf("callChannelRPC(): %v", err)
	}
	if resp.Channel == nil || resp.Channel.ChannelID != "channel-a" {
		t.Fatalf("response channel = %+v", resp.Channel)
	}
	if want := []multiraft.NodeID{2, 3}; !equalNodeIDs(calls, want) {
		t.Fatalf("RPC attempts = %v, want %v", calls, want)
	}
}

func TestAuthoritativeRPCFallsBackAfterMalformedLeaderResponse(t *testing.T) {
	var calls []multiraft.NodeID
	cluster := &proxyTestMigrationCluster{
		localNodeID: 3,
		leaders:     map[multiraft.SlotID]multiraft.NodeID{7: 2},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{7: {1, 2}},
	}
	cluster.rpcService = func(_ context.Context, nodeID multiraft.NodeID, _ multiraft.SlotID, _ uint8, _ []byte) ([]byte, error) {
		calls = append(calls, nodeID)
		if nodeID == 2 {
			return []byte("truncated"), nil
		}
		return encodeChannelRPCResponse(channelRPCResponse{Status: rpcStatusNotFound})
	}

	resp, err := (&Store{cluster: cluster}).callChannelRPC(context.Background(), 7, channelRPCRequest{
		Op: channelRPCGetForPermission, SlotID: 7, ChannelID: "missing", ChannelType: 2,
	})
	if err != nil {
		t.Fatalf("callChannelRPC(): %v", err)
	}
	if resp.Status != rpcStatusNotFound {
		t.Fatalf("response status = %q, want %q", resp.Status, rpcStatusNotFound)
	}
	if want := []multiraft.NodeID{2, 1}; !equalNodeIDs(calls, want) {
		t.Fatalf("RPC attempts = %v, want %v", calls, want)
	}
}

func TestAuthoritativeRPCResponseFamiliesExposeRedirectLeader(t *testing.T) {
	const leaderID = uint64(41)
	responses := []authoritativeRPCResponse{
		channelRPCResponse{LeaderID: leaderID},
		identityRPCResponse{LeaderID: leaderID},
		subscriberRPCResponse{LeaderID: leaderID},
		membershipRPCResponse{LeaderID: leaderID},
		permissionBatchRPCResponse{LeaderID: leaderID},
		pluginBindingRPCResponse{LeaderID: leaderID},
		runtimeMetaRPCResponse{LeaderID: leaderID},
		channelMigrationRPCResponse{LeaderID: leaderID},
	}
	for _, response := range responses {
		if got := response.rpcLeaderID(); got != leaderID {
			t.Fatalf("%T redirect leader = %d, want %d", response, got, leaderID)
		}
	}
}

func TestHandleAuthoritativeRPCMapsLeadershipStateAndEncodingErrors(t *testing.T) {
	tests := []struct {
		name       string
		leaderID   multiraft.NodeID
		leaderErr  error
		localID    multiraft.NodeID
		wantStatus string
		wantLeader uint64
		wantHandle bool
	}{
		{name: "missing slot", leaderErr: fmt.Errorf("lookup failed: %w", ErrSlotNotFound), wantStatus: rpcStatusNoSlot, wantHandle: true},
		{name: "election pending", leaderErr: errors.New("election pending"), wantStatus: rpcStatusNoLeader, wantHandle: true},
		{name: "remote leader", leaderID: 9, localID: 1, wantStatus: rpcStatusNotLeader, wantLeader: 9, wantHandle: true},
		{name: "local leader", leaderID: 1, localID: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := &leaderStateContractCluster{
				proxyTestMigrationCluster: &proxyTestMigrationCluster{localNodeID: test.localID},
				leaderID:                  test.leaderID,
				leaderErr:                 test.leaderErr,
			}
			var gotStatus string
			var gotLeader uint64
			body, handled, err := (&Store{cluster: cluster}).handleAuthoritativeRPC(7, func(status string, leaderID uint64) ([]byte, error) {
				gotStatus, gotLeader = status, leaderID
				return []byte(status), nil
			})
			if err != nil {
				t.Fatalf("handleAuthoritativeRPC(): %v", err)
			}
			if handled != test.wantHandle || gotStatus != test.wantStatus || gotLeader != test.wantLeader {
				t.Fatalf("handled/status/leader = %v/%q/%d, want %v/%q/%d", handled, gotStatus, gotLeader, test.wantHandle, test.wantStatus, test.wantLeader)
			}
			if handled && string(body) != test.wantStatus {
				t.Fatalf("encoded status = %q, want %q", body, test.wantStatus)
			}
		})
	}

	encodeErr := errors.New("encode status")
	cluster := &leaderStateContractCluster{
		proxyTestMigrationCluster: &proxyTestMigrationCluster{localNodeID: 1},
		leaderID:                  9,
	}
	_, handled, err := (&Store{cluster: cluster}).handleAuthoritativeRPC(7, func(string, uint64) ([]byte, error) {
		return nil, encodeErr
	})
	if !handled || !errors.Is(err, encodeErr) {
		t.Fatalf("handled/error = %v/%v, want true/%v", handled, err, encodeErr)
	}
}

func TestPluginBindingPageCursorRejectsTruncatedAndSemanticallyInvalidTokens(t *testing.T) {
	cursor := pluginBindingPageCursor{
		SlotID: 7, HashSlot: 23,
		Binding: metadb.PluginUserBindingCursor{PluginNo: "plugin-a", UID: "user-a"},
	}
	raw, err := encodePluginBindingPageCursor(cursor)
	if err != nil {
		t.Fatalf("encodePluginBindingPageCursor(): %v", err)
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode valid base64 cursor: %v", err)
	}
	for cut := 1; cut < len(body); cut++ {
		truncated := base64.RawURLEncoding.EncodeToString(body[:cut])
		if _, err := decodePluginBindingPageCursor(truncated); err == nil {
			t.Fatalf("truncated cursor accepted at byte %d of %d", cut, len(body))
		}
	}
	withTrailingByte := base64.RawURLEncoding.EncodeToString(append(append([]byte(nil), body...), 0))
	if _, err := decodePluginBindingPageCursor(withTrailingByte); err == nil {
		t.Fatal("cursor with trailing data accepted")
	}

	invalidCursors := []pluginBindingPageCursor{
		{HashSlot: 23, Binding: cursor.Binding},
		{SlotID: 7, HashSlot: 23, Binding: metadb.PluginUserBindingCursor{UID: "user-a"}},
		{SlotID: 7, HashSlot: 23, Binding: metadb.PluginUserBindingCursor{PluginNo: "plugin-a"}},
	}
	for _, invalid := range invalidCursors {
		invalidBody := append([]byte(nil), pluginBindingPageCursorMagic[:]...)
		invalidBody = runtimeMetaAppendUvarint(invalidBody, uint64(invalid.SlotID))
		invalidBody = runtimeMetaAppendUvarint(invalidBody, uint64(invalid.HashSlot))
		invalidBody = runtimeMetaAppendString(invalidBody, invalid.Binding.PluginNo)
		invalidBody = runtimeMetaAppendString(invalidBody, invalid.Binding.UID)
		if _, err := decodePluginBindingPageCursor(base64.RawURLEncoding.EncodeToString(invalidBody)); !errors.Is(err, metadb.ErrInvalidArgument) {
			t.Fatalf("invalid cursor %+v error = %v, want ErrInvalidArgument", invalid, err)
		}
	}
}

func TestRuntimeMetadataBatchPreservesPartialMissesAndPropagatesRPCFailure(t *testing.T) {
	keys := []metadb.ChannelKey{
		{ChannelID: "channel-a", ChannelType: 2},
		{ChannelID: "channel-missing", ChannelType: 2},
	}
	cluster := &proxyTestMigrationCluster{
		localNodeID: 1,
		slotForKey:  7,
		leaders:     map[multiraft.SlotID]multiraft.NodeID{7: 2},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{7: {2}},
	}
	cluster.rpcService = func(_ context.Context, _ multiraft.NodeID, _ multiraft.SlotID, _ uint8, payload []byte) ([]byte, error) {
		req, err := decodeRuntimeMetaRPCRequest(payload)
		if err != nil {
			return nil, err
		}
		if req.Op != runtimeMetaRPCBatchGet || len(req.Keys) != len(keys) {
			return nil, fmt.Errorf("unexpected batch request: %+v", req)
		}
		return encodeRuntimeMetaRPCResponseForVersion(runtimeMetaRPCResponse{
			Status: rpcStatusOK,
			Metas: []metadb.ChannelRuntimeMeta{{
				ChannelID: "channel-a", ChannelType: 2, LeaderEpoch: 8, RouteGeneration: 8,
			}},
		}, req.CodecVersion)
	}
	store := &Store{cluster: cluster, db: new(metadb.DB)}
	results, err := store.ReadChannelRuntimeMetadataBatch(context.Background(), keys)
	if err != nil {
		t.Fatalf("ReadChannelRuntimeMetadataBatch(): %v", err)
	}
	if len(results) != 2 || results[0].Err != nil || results[0].Meta.ChannelID != "channel-a" || !errors.Is(results[1].Err, metadb.ErrNotFound) {
		t.Fatalf("batch results = %+v", results)
	}
	metas, err := store.BatchGetChannelRuntimeMetas(context.Background(), keys)
	if err != nil {
		t.Fatalf("BatchGetChannelRuntimeMetas(): %v", err)
	}
	if len(metas) != 1 || metas[keys[0]].ChannelID != "channel-a" {
		t.Fatalf("batch compatibility map = %+v", metas)
	}

	rpcErr := errors.New("slot RPC unavailable")
	cluster.rpcService = func(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error) {
		return nil, rpcErr
	}
	results, err = store.ReadChannelRuntimeMetadataBatch(context.Background(), keys)
	if err != nil {
		t.Fatalf("item-scoped batch failure returned top-level error: %v", err)
	}
	for i := range results {
		if !errors.Is(results[i].Err, rpcErr) {
			t.Fatalf("result %d error = %v, want %v", i, results[i].Err, rpcErr)
		}
	}
	if _, err := store.BatchGetChannelRuntimeMetas(context.Background(), keys); !errors.Is(err, rpcErr) {
		t.Fatalf("compatibility batch error = %v, want %v", err, rpcErr)
	}
}

func TestPermissionBatchRejectsUnreadyAndOversizedRequestsPerItem(t *testing.T) {
	reads := []PermissionMetadataRead{
		{Kind: PermissionMetadataReadChannel, ChannelID: "channel-a", ChannelType: 2},
		{Kind: PermissionMetadataReadSubscriberHasAny, ChannelID: "channel-b", ChannelType: 2},
	}
	results := (*Store)(nil).ReadPermissionMetadataBatch(context.Background(), reads)
	for i := range results {
		if results[i].Err == nil {
			t.Fatalf("unready result %d has no error", i)
		}
	}

	oversized := make([]PermissionMetadataRead, permissionBatchMaxReads+1)
	store := &Store{cluster: &proxyTestMigrationCluster{}, db: new(metadb.DB)}
	results = store.ReadPermissionMetadataBatch(context.Background(), oversized)
	if len(results) != len(oversized) {
		t.Fatalf("oversized result count = %d, want %d", len(results), len(oversized))
	}
	if results[0].Err == nil || results[len(results)-1].Err == nil {
		t.Fatal("oversized boundary did not mark every result as failed")
	}
}

func TestGuardedMigrationMutationsPreserveRouteTermAndCommand(t *testing.T) {
	guard := metadb.ChannelMigrationTaskGuard{
		ChannelID: "channel-route", ChannelType: 2, TaskID: "task-a",
		ExpectedStatus: metadb.ChannelMigrationStatusRunning,
		ExpectedPhase:  metadb.ChannelMigrationPhaseFinalTargetCatchUp,
	}
	runtimeGuard := metadb.ChannelMigrationRuntimeGuard{
		ChannelID: "channel-route", ChannelType: 2,
		ExpectedChannelEpoch: 12, ExpectedLeaderEpoch: 17, ExpectedLeader: 1,
		ExpectedFenceToken: "task-a", ExpectedFenceVersion: 4, ExpectedRouteGeneration: 17,
	}

	tests := []struct {
		name        string
		wantCommand []byte
		run         func(*Store) error
	}{
		{
			name: "set fence",
			wantCommand: metafsm.EncodeSetChannelWriteFenceCommand(metadb.ChannelMigrationFenceRequest{
				Guard: guard, RuntimeGuard: runtimeGuard,
			}),
			run: func(store *Store) error {
				return store.SetChannelWriteFence(context.Background(), metadb.ChannelMigrationFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard})
			},
		},
		{
			name: "reset fence recovery",
			wantCommand: metafsm.EncodeResetChannelWriteFenceToPreCutoverCommand(metadb.ChannelMigrationResetFenceRequest{
				Guard: guard, RuntimeGuard: runtimeGuard,
			}),
			run: func(store *Store) error {
				return store.ResetChannelWriteFenceToPreCutover(context.Background(), metadb.ChannelMigrationResetFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard})
			},
		},
		{
			name: "commit leader transfer",
			wantCommand: metafsm.EncodeCommitChannelLeaderTransferCommand(metadb.ChannelMigrationLeaderTransferRequest{
				Guard: guard, RuntimeGuard: runtimeGuard, DesiredLeader: 3, NextLeaderEpoch: 18,
			}),
			run: func(store *Store) error {
				return store.CommitChannelLeaderTransfer(context.Background(), metadb.ChannelMigrationLeaderTransferRequest{
					Guard: guard, RuntimeGuard: runtimeGuard, DesiredLeader: 3, NextLeaderEpoch: 18,
				})
			},
		},
		{
			name: "add learner",
			wantCommand: metafsm.EncodeAddChannelLearnerCommand(metadb.ChannelMigrationAddLearnerRequest{
				Guard: guard, RuntimeGuard: runtimeGuard, TargetNode: 3,
			}),
			run: func(store *Store) error {
				return store.AddChannelLearner(context.Background(), metadb.ChannelMigrationAddLearnerRequest{
					Guard: guard, RuntimeGuard: runtimeGuard, TargetNode: 3,
				})
			},
		},
		{
			name: "clear fence",
			wantCommand: metafsm.EncodeClearChannelWriteFenceCommand(metadb.ChannelMigrationClearFenceRequest{
				Guard: guard, RuntimeGuard: runtimeGuard,
			}),
			run: func(store *Store) error {
				return store.ClearChannelWriteFence(context.Background(), metadb.ChannelMigrationClearFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard})
			},
		},
		{
			name: "abort",
			wantCommand: metafsm.EncodeAbortChannelMigrationCommand(metadb.ChannelMigrationAbortRequest{
				Guard: guard, RuntimeGuard: runtimeGuard, LastError: "operator abort",
			}),
			run: func(store *Store) error {
				return store.AbortChannelMigration(context.Background(), metadb.ChannelMigrationAbortRequest{
					Guard: guard, RuntimeGuard: runtimeGuard, LastError: "operator abort",
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := newGuardedMutationContractCluster()
			if err := test.run(&Store{cluster: cluster}); err != nil {
				t.Fatalf("guarded mutation: %v", err)
			}
			if len(cluster.proposalCalls) != 1 {
				t.Fatalf("proposal calls = %d, want 1", len(cluster.proposalCalls))
			}
			call := cluster.proposalCalls[0]
			if call.slotID != 7 || call.hashSlot != 23 || !bytes.Equal(call.command, test.wantCommand) {
				t.Fatalf("proposal = slot %d hash %d command %x, want slot 7 hash 23 command %x", call.slotID, call.hashSlot, call.command, test.wantCommand)
			}
			if len(cluster.slotKeys) != 1 || cluster.slotKeys[0] != guard.ChannelID || len(cluster.hashKeys) != 1 || cluster.hashKeys[0] != guard.ChannelID {
				t.Fatalf("routing keys = slot %v hash %v, want %q", cluster.slotKeys, cluster.hashKeys, guard.ChannelID)
			}
		})
	}

	proposalErr := errors.New("proposal rejected")
	cluster := newGuardedMutationContractCluster()
	cluster.proposalErr = proposalErr
	err := (&Store{cluster: cluster}).SetChannelWriteFence(context.Background(), metadb.ChannelMigrationFenceRequest{Guard: guard, RuntimeGuard: runtimeGuard})
	if !errors.Is(err, proposalErr) {
		t.Fatalf("SetChannelWriteFence() error = %v, want %v", err, proposalErr)
	}
}

func TestStoreRPCHandlerForwardsPayloadAndError(t *testing.T) {
	wantErr := errors.New("handler failure")
	handler := storeRPCHandlerFunc(func(_ context.Context, payload []byte) ([]byte, error) {
		if string(payload) != "request" {
			t.Fatalf("payload = %q, want request", payload)
		}
		return []byte("response"), wantErr
	})
	body, err := handler.HandleRPC(context.Background(), []byte("request"))
	if string(body) != "response" || !errors.Is(err, wantErr) {
		t.Fatalf("handler result = %q/%v, want response/%v", body, err, wantErr)
	}
}

func TestRouteErrorClassifiersRecognizeWrappedCanonicalAliases(t *testing.T) {
	if !isNoLeader(errors.New("rpc failed: cluster: no slot leader")) {
		t.Fatal("wrapped no-leader alias was not classified")
	}
	if !isNotLeader(errors.New("rpc failed: cluster/propose: not leader")) {
		t.Fatal("wrapped not-leader alias was not classified")
	}
}

func equalNodeIDs(left, right []multiraft.NodeID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type leaderStateContractCluster struct {
	*proxyTestMigrationCluster
	leaderID  multiraft.NodeID
	leaderErr error
}

func (c *leaderStateContractCluster) LeaderOf(multiraft.SlotID) (multiraft.NodeID, error) {
	return c.leaderID, c.leaderErr
}

type proposalContractCall struct {
	slotID   multiraft.SlotID
	hashSlot uint16
	command  []byte
}

type guardedMutationContractCluster struct {
	*proxyTestMigrationCluster
	slotKeys      []string
	hashKeys      []string
	proposalCalls []proposalContractCall
	proposalErr   error
}

func newGuardedMutationContractCluster() *guardedMutationContractCluster {
	return &guardedMutationContractCluster{proxyTestMigrationCluster: &proxyTestMigrationCluster{
		localNodeID: 1,
		leaders:     map[multiraft.SlotID]multiraft.NodeID{7: 1},
	}}
}

func (c *guardedMutationContractCluster) SlotForKey(key string) multiraft.SlotID {
	c.slotKeys = append(c.slotKeys, key)
	return 7
}

func (c *guardedMutationContractCluster) HashSlotForKey(key string) uint16 {
	c.hashKeys = append(c.hashKeys, key)
	return 23
}

func (c *guardedMutationContractCluster) ProposeWithHashSlot(_ context.Context, slotID multiraft.SlotID, hashSlot uint16, command []byte) error {
	c.proposalCalls = append(c.proposalCalls, proposalContractCall{
		slotID: slotID, hashSlot: hashSlot, command: append([]byte(nil), command...),
	})
	return c.proposalErr
}
