//go:build integration

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases are fixed in docs/reports/2026-09-24-send-ban-rpc-boundaries.md.
// These exercise the real proxy/codec/snapshot with controlled cluster ports;
// they do not claim real-network or Raft-quorum performance qualification.
func TestSendPermissionRPCBoundaries(t *testing.T) {
	var evidence []map[string]any
	started := time.Now().UTC()
	t.Cleanup(func() {
		path := os.Getenv("WK_SEND_PERMISSION_BOUNDARY_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "send-permission-boundaries.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "source_revision": os.Getenv("WK_VALIDATION_SOURCE_REVISION"), "source_fingerprint": os.Getenv("WK_VALIDATION_SOURCE_FINGERPRINT"), "kind": "isolated proxy/codec/snapshot integration; controlled cluster ports", "cases": evidence}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0644))
		t.Logf("boundary report: %s", path)
	})
	t.Run("4096-facts-chunk-and-align", func(t *testing.T) {
		db := boundaryDB(t)
		reads := make([]PermissionMetadataRead, 4096)
		routes := make(map[string]SendPermissionRoute, len(reads))
		wb := db.NewWriteBatch()
		for i := range reads {
			uid := fmt.Sprintf("boundary-user-%04d", i)
			reads[i] = PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: uid}
			hs := uint16(i % 256)
			routes[uid] = boundaryRoute(hs, uint64(hs%16+1), 2, 1)
			if i%2 == 0 {
				_, err := wb.ApplySendBan(metadb.HashSlot(hs), metadb.SendBanMutation{UID: uid, SendBan: 1})
				require.NoError(t, err)
			}
		}
		require.NoError(t, wb.Commit())
		require.NoError(t, wb.Close())
		route := func(keys []string) []SendPermissionRoute {
			out := make([]SendPermissionRoute, len(keys))
			for i, key := range keys {
				out[i] = routes[key]
			}
			return out
		}
		remoteCluster := newBoundaryCluster(2)
		remoteCluster.routes = route
		remote := New(remoteCluster, db)
		originCluster := newBoundaryCluster(1)
		originCluster.routes = route
		gate := boundaryGate(t)
		entered := make(chan struct{}, 64)
		var active, peak atomic.Int64
		var mu sync.Mutex
		var requests, replies []int
		seen := make([]int, len(reads))
		originCluster.rpc = func(ctx context.Context, _ multiraft.NodeID, raw []byte) ([]byte, error) {
			var q sendPermissionRequest
			if err := decodeSendPermissionJSON(raw, &q); err != nil {
				return nil, err
			}
			n := active.Add(1)
			raiseBoundaryPeak(&peak, n)
			defer active.Add(-1)
			mu.Lock()
			requests = append(requests, len(raw))
			for _, g := range q.Groups {
				for _, r := range g.Reads {
					if r.Index >= 0 && r.Index < len(seen) {
						seen[r.Index]++
					}
				}
			}
			mu.Unlock()
			entered <- struct{}{}
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			reply, err := remote.handleSendPermissionRPC(ctx, raw)
			mu.Lock()
			replies = append(replies, len(reply))
			mu.Unlock()
			return reply, err
		}
		result := startBoundaryRead(t, New(originCluster, db), reads)
		for range 4 {
			awaitBoundaryEvent(t, entered)
		}
		assertBoundaryNoExtra(t, entered)
		closeBoundaryGate(gate)
		out := awaitBoundaryResult(t, result)
		require.Len(t, out, len(reads))
		for i, r := range out {
			require.NoError(t, r.Err)
			require.Equal(t, i%2 == 0, r.Found)
			require.EqualValues(t, 1-i%2, r.UserPolicy.SendBan)
			require.EqualValues(t, 1-i%2, r.UserPolicy.Version)
			require.Equal(t, 1, seen[i], "input %d", i)
		}
		require.Greater(t, len(requests), 1)
		require.Equal(t, len(requests), len(replies))
		for _, n := range append(append([]int{}, requests...), replies...) {
			require.LessOrEqual(t, n, sendPermissionMaxBytes)
		}
		require.EqualValues(t, 4, peak.Load())
		require.Zero(t, active.Load())
		require.Zero(t, remote.permissionInflight.Load())
		require.Zero(t, remoteCluster.admitted.Load())
		evidence = append(evidence, map[string]any{"name": t.Name(), "facts": len(reads), "hash_slots": 256, "physical_slots": 16, "node_envelopes": len(requests), "request_bytes": requests, "response_bytes": replies, "outbound_peak": peak.Load(), "every_input_once": true})
	})
	t.Run("exact-wire-byte-limit", func(t *testing.T) {
		db := boundaryDB(t)
		remote := New(newBoundaryCluster(2), db)
		q := sendPermissionRequest{Format: 1, Groups: []sendPermissionGroup{{Fence: boundaryRoute(1, 1, 2, 1).Fence, Reads: []sendPermissionRead{{Index: 0, HashSlot: 1, Read: PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}}}}}
		raw, err := json.Marshal(q)
		require.NoError(t, err)
		padded := append(raw, bytes.Repeat([]byte(" "), sendPermissionMaxBytes-len(raw))...)
		reply, err := remote.handleSendPermissionRPC(context.Background(), padded)
		require.NoError(t, err)
		var got sendPermissionReply
		require.NoError(t, decodeSendPermissionJSON(reply, &got))
		require.NoError(t, validateSendPermissionReply(q, got))
		_, err = remote.handleSendPermissionRPC(context.Background(), append(padded, ' '))
		require.ErrorIs(t, err, metadb.ErrInvalidArgument)
		require.Zero(t, remote.permissionInflight.Load())
		origin := newBoundaryCluster(1)
		responseBytes := sendPermissionMaxBytes
		origin.rpc = func(ctx context.Context, _ multiraft.NodeID, raw []byte) ([]byte, error) {
			body, err := remote.handleSendPermissionRPC(ctx, raw)
			if err != nil {
				return nil, err
			}
			return append(body, bytes.Repeat([]byte(" "), responseBytes-len(body))...), nil
		}
		store := New(origin, db)
		reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
		out := store.ReadSendPermissionMetadataBatch(context.Background(), reads)
		require.NoError(t, out[0].Err)
		responseBytes++
		out = store.ReadSendPermissionMetadataBatch(context.Background(), reads)
		require.ErrorIs(t, out[0].Err, metadb.ErrInvalidArgument)
		require.EqualValues(t, 2, origin.calls.Load())
		evidence = append(evidence, map[string]any{"name": t.Name(), "request_at_limit": "accepted", "response_at_limit": "accepted", "one_byte_over_limit": "rejected", "byte_limit": sendPermissionMaxBytes})
	})
	t.Run("five-node-parallel-outbound", func(t *testing.T) {
		db := boundaryDB(t)
		routes := make(map[string]SendPermissionRoute)
		var reads []PermissionMetadataRead
		for i := 0; i < 5; i++ {
			key := fmt.Sprintf("node-user-%d", i)
			reads = append(reads, PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: key})
			routes[key] = boundaryRoute(uint16(i), uint64(i+1), uint64(i+2), 1)
		}
		route := func(keys []string) []SendPermissionRoute {
			out := make([]SendPermissionRoute, len(keys))
			for i, key := range keys {
				out[i] = routes[key]
			}
			return out
		}
		remotes := make(map[multiraft.NodeID]*Store)
		for i := 2; i <= 6; i++ {
			c := newBoundaryCluster(multiraft.NodeID(i))
			c.routes = route
			remotes[c.node] = New(c, db)
		}
		gate := boundaryGate(t)
		entered := make(chan struct{}, 5)
		var active, peak atomic.Int64
		var mu sync.Mutex
		seen := map[multiraft.NodeID]int{}
		origin := newBoundaryCluster(1)
		origin.routes = route
		origin.rpc = func(ctx context.Context, node multiraft.NodeID, raw []byte) ([]byte, error) {
			n := active.Add(1)
			raiseBoundaryPeak(&peak, n)
			defer active.Add(-1)
			mu.Lock()
			seen[node]++
			mu.Unlock()
			entered <- struct{}{}
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return remotes[node].handleSendPermissionRPC(ctx, raw)
		}
		result := startBoundaryRead(t, New(origin, db), reads)
		for range 4 {
			awaitBoundaryEvent(t, entered)
		}
		assertBoundaryNoExtra(t, entered)
		closeBoundaryGate(gate)
		out := awaitBoundaryResult(t, result)
		for _, r := range out {
			require.NoError(t, r.Err)
		}
		require.Len(t, seen, 5)
		for _, n := range seen {
			require.Equal(t, 1, n)
		}
		require.EqualValues(t, 4, peak.Load())
		require.Zero(t, active.Load())
		evidence = append(evidence, map[string]any{"name": t.Name(), "remote_nodes": 5, "node_envelopes": 5, "outbound_peak": peak.Load()})
	})
	t.Run("sixteen-slots-four-barriers", func(t *testing.T) {
		db := boundaryDB(t)
		c := newBoundaryCluster(2)
		routes := map[string]SendPermissionRoute{}
		var reads []PermissionMetadataRead
		for i := 0; i < 16; i++ {
			key := fmt.Sprintf("slot-user-%d", i)
			reads = append(reads, PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: key})
			routes[key] = boundaryRoute(uint16(i), uint64(i+1), 2, 1)
		}
		c.routes = func(keys []string) []SendPermissionRoute {
			out := make([]SendPermissionRoute, len(keys))
			for i, key := range keys {
				out[i] = routes[key]
			}
			return out
		}
		gate := boundaryGate(t)
		entered := make(chan struct{}, 16)
		var active, peak, calls atomic.Int64
		c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
			calls.Add(1)
			n := active.Add(1)
			raiseBoundaryPeak(&peak, n)
			defer active.Add(-1)
			entered <- struct{}{}
			select {
			case <-gate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		s := New(c, db)
		result := startBoundaryRead(t, s, reads)
		for range 4 {
			awaitBoundaryEvent(t, entered)
		}
		assertBoundaryNoExtra(t, entered)
		closeBoundaryGate(gate)
		out := awaitBoundaryResult(t, result)
		for _, r := range out {
			require.NoError(t, r.Err)
		}
		require.EqualValues(t, 4, peak.Load())
		require.EqualValues(t, 16, calls.Load())
		require.Zero(t, c.calls.Load())
		require.Zero(t, active.Load())
		require.Zero(t, c.admitted.Load())
		require.Zero(t, s.permissionInflight.Load())
		evidence = append(evidence, map[string]any{"name": t.Name(), "slot_groups": 16, "barriers": calls.Load(), "barrier_peak": peak.Load(), "loopback_rpc": c.calls.Load()})
	})
	t.Run("retry-only-stale-group", func(t *testing.T) {
		for _, alwaysStale := range []bool{false, true} {
			t.Run(fmt.Sprintf("stale-again-%t", alwaysStale), func(t *testing.T) {
				db := boundaryDB(t)
				var revision atomic.Uint64
				revision.Store(1)
				route := func(keys []string) []SendPermissionRoute {
					out := make([]SendPermissionRoute, len(keys))
					for i, key := range keys {
						slot, rev := uint64(1), uint64(1)
						if key != "u" {
							slot, rev = 2, revision.Load()
						}
						out[i] = boundaryRoute(uint16(slot), slot, 2, rev)
					}
					return out
				}
				remoteCluster := newBoundaryCluster(2)
				remoteCluster.routes = route
				var barriers [3]atomic.Int64
				remoteCluster.barrier = func(_ context.Context, slot multiraft.SlotID) error { barriers[slot].Add(1); return nil }
				remote := New(remoteCluster, db)
				origin := newBoundaryCluster(1)
				origin.routes = route
				var indexes [][]int
				origin.rpc = func(ctx context.Context, _ multiraft.NodeID, raw []byte) ([]byte, error) {
					var q sendPermissionRequest
					if err := decodeSendPermissionJSON(raw, &q); err != nil {
						return nil, err
					}
					var batch []int
					for _, g := range q.Groups {
						for _, r := range g.Reads {
							batch = append(batch, r.Index)
						}
					}
					indexes = append(indexes, batch)
					if len(indexes) == 1 || alwaysStale {
						revision.Add(1)
					}
					return remote.handleSendPermissionRPC(ctx, raw)
				}
				out := New(origin, db).ReadSendPermissionMetadataBatch(context.Background(), []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}, {Kind: PermissionMetadataReadUserSendPolicy, UID: "v"}})
				require.NoError(t, out[0].Err)
				if alwaysStale {
					require.ErrorIs(t, out[1].Err, ErrReadStaleRoute)
				} else {
					require.NoError(t, out[1].Err)
				}
				require.Equal(t, [][]int{{0, 1}, {1}}, indexes)
				require.EqualValues(t, 1, barriers[1].Load())
				want := int64(1)
				if alwaysStale {
					want = 0
				}
				require.Equal(t, want, barriers[2].Load())
				evidence = append(evidence, map[string]any{"name": t.Name(), "rpc_input_indexes": indexes, "successful_group_barriers": barriers[1].Load(), "retried_group_barriers": barriers[2].Load()})
			})
		}
	})
	t.Run("corrupt-response-fails-closed", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*sendPermissionReply)
		}{
			{"duplicate-index", func(r *sendPermissionReply) { r.Groups[1].Results[1] = r.Groups[1].Results[0] }},
			{"duplicate-group", func(r *sendPermissionReply) { r.Groups[1] = r.Groups[0] }},
			{"stale-with-facts", func(r *sendPermissionReply) { r.Groups[0].Status = "stale_route" }},
			{"missing-value", func(r *sendPermissionReply) { r.Groups[1].Results = r.Groups[1].Results[:1] }},
			{"extra-value", func(r *sendPermissionReply) {
				r.Groups[0].Results = append(r.Groups[0].Results, r.Groups[0].Results[0])
			}},
			{"missing-banned-user", func(r *sendPermissionReply) { r.Groups[0].Results[0].Fact.UserPolicy.SendBan = 1 }},
			{"changed-fence", func(r *sendPermissionReply) { r.Groups[0].Fence.LeaderTerm++ }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				db := boundaryDB(t)
				remote := New(newBoundaryCluster(2), db)
				origin := newBoundaryCluster(1)
				var calls int
				origin.rpc = func(ctx context.Context, _ multiraft.NodeID, raw []byte) ([]byte, error) {
					calls++
					body, err := remote.handleSendPermissionRPC(ctx, raw)
					if err != nil {
						return nil, err
					}
					var reply sendPermissionReply
					if err := decodeSendPermissionJSON(body, &reply); err != nil {
						return nil, err
					}
					tc.mutate(&reply)
					return json.Marshal(reply)
				}
				out := New(origin, db).ReadSendPermissionMetadataBatch(context.Background(), []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}, {Kind: PermissionMetadataReadUserSendPolicy, UID: "v"}, {Kind: PermissionMetadataReadUserSendPolicy, UID: "w"}})
				require.Len(t, out, 3)
				for _, r := range out {
					require.ErrorIs(t, r.Err, metadb.ErrCorruptValue)
					require.False(t, r.Found)
					require.Zero(t, r.UserPolicy)
				}
				require.Equal(t, 1, calls)
				evidence = append(evidence, map[string]any{"name": t.Name(), "rpc_calls": calls, "all_items_rejected": true})
			})
		}
	})
	t.Run("global-admission-cancel-and-reuse", func(t *testing.T) {
		db := boundaryDB(t)
		c := newBoundaryCluster(2)
		gate := boundaryGate(t)
		entered := make(chan struct{}, 2*sendPermissionMaxExecuting)
		var active, peak atomic.Int64
		c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
			n := active.Add(1)
			raiseBoundaryPeak(&peak, n)
			defer active.Add(-1)
			entered <- struct{}{}
			select {
			case <-gate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		s := New(c, db)
		q := sendPermissionRequest{Format: 1, Groups: []sendPermissionGroup{{Fence: boundaryRoute(1, 1, 2, 1).Fence, Reads: []sendPermissionRead{{Index: 0, HashSlot: 1, Read: PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}}}}}
		raw, err := json.Marshal(q)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		first, cancelFirst := context.WithCancel(ctx)
		defer cancelFirst()
		type answer struct {
			body []byte
			err  error
		}
		launch := func(ctx context.Context) <-chan answer {
			done := make(chan answer, 1)
			go func() { body, err := s.handleSendPermissionRPC(ctx, raw); done <- answer{body, err} }()
			return done
		}
		var pending []<-chan answer
		pending = append(pending, launch(first))
		for range sendPermissionMaxExecuting - 1 {
			pending = append(pending, launch(ctx))
		}
		// Cancel and join before the database cleanup, even if an assertion fails.
		var joined [sendPermissionMaxExecuting + 1]bool
		t.Cleanup(func() {
			cancel()
			closeBoundaryGate(gate)
			for i, ch := range pending {
				if !joined[i] {
					select {
					case <-ch:
					case <-time.After(2 * time.Second):
						t.Errorf("envelope %d failed to join", i)
					}
				}
			}
		})
		for range sendPermissionMaxExecuting {
			awaitBoundaryEvent(t, entered)
		}
		require.EqualValues(t, sendPermissionMaxExecuting, s.permissionInflight.Load())
		require.EqualValues(t, sendPermissionMaxExecuting, c.admitted.Load())
		body, err := s.handleSendPermissionRPC(ctx, raw)
		require.NoError(t, err)
		var busy sendPermissionReply
		require.NoError(t, decodeSendPermissionJSON(body, &busy))
		require.ErrorIs(t, validateSendPermissionReply(q, busy), ErrPermissionBusy)
		_, err = s.serveSendPermissions(ctx, q)
		require.ErrorIs(t, err, ErrPermissionBusy)
		require.EqualValues(t, sendPermissionMaxExecuting, active.Load())
		cancelFirst()
		var canceled answer
		select {
		case canceled = <-pending[0]:
			joined[0] = true
		case <-time.After(2 * time.Second):
			t.Fatal("canceled envelope did not complete")
		}
		require.NoError(t, canceled.err)
		var canceledReply sendPermissionReply
		require.NoError(t, decodeSendPermissionJSON(canceled.body, &canceledReply))
		require.Equal(t, "unavailable", canceledReply.Groups[0].Status)
		require.EqualValues(t, sendPermissionMaxExecuting-1, s.permissionInflight.Load())
		pending = append(pending, launch(ctx))
		awaitBoundaryEvent(t, entered)
		require.EqualValues(t, sendPermissionMaxExecuting, s.permissionInflight.Load())
		closeBoundaryGate(gate)
		for i, ch := range pending {
			if joined[i] {
				continue
			}
			select {
			case got := <-ch:
				joined[i] = true
				require.NoError(t, got.err)
				var reply sendPermissionReply
				require.NoError(t, decodeSendPermissionJSON(got.body, &reply))
				require.NoError(t, validateSendPermissionReply(q, reply))
				require.Equal(t, "ok", reply.Groups[0].Status)
			case <-time.After(2 * time.Second):
				t.Fatal("envelope did not complete")
			}
		}
		require.EqualValues(t, sendPermissionMaxExecuting, peak.Load())
		require.Zero(t, s.permissionInflight.Load())
		require.Zero(t, c.admitted.Load())
		require.Zero(t, active.Load())
		evidence = append(evidence, map[string]any{"name": t.Name(), "admitted_peak": peak.Load(), "remote_overflow": "busy", "local_overflow": "busy", "canceled_permit_reused": true, "residual_inflight": s.permissionInflight.Load()})
	})
}

// boundaryCluster varies only the cluster seams; production Store/codec/read
// implementations remain unchanged. All callbacks are installed before use.
type boundaryCluster struct {
	*sendPermissionTestCluster
	routes  func([]string) []SendPermissionRoute
	rpc     func(context.Context, multiraft.NodeID, []byte) ([]byte, error)
	barrier func(context.Context, multiraft.SlotID) error
}

func newBoundaryCluster(node multiraft.NodeID) *boundaryCluster {
	return &boundaryCluster{sendPermissionTestCluster: &sendPermissionTestCluster{node: node}}
}
func (c *boundaryCluster) SendPermissionRoutes(keys []string) []SendPermissionRoute {
	if c.routes != nil {
		return c.routes(keys)
	}
	return c.sendPermissionTestCluster.SendPermissionRoutes(keys)
}
func (c *boundaryCluster) RPCService(ctx context.Context, node multiraft.NodeID, _ multiraft.SlotID, _ uint8, raw []byte) ([]byte, error) {
	c.calls.Add(1)
	if c.rpc == nil {
		return nil, fmt.Errorf("unexpected loopback RPC")
	}
	return c.rpc(ctx, node, raw)
}
func (c *boundaryCluster) ReadSlotBarrier(ctx context.Context, slot multiraft.SlotID) error {
	if c.barrier != nil {
		return c.barrier(ctx, slot)
	}
	return ctx.Err()
}
func boundaryRoute(hash uint16, slot, node, revision uint64) SendPermissionRoute {
	return SendPermissionRoute{HashSlot: hash, Fence: SendPermissionFence{SlotID: slot, LeaderNodeID: node, LeaderTerm: 1, ConfigEpoch: 1, RouteRevision: revision}}
}
func boundaryDB(t *testing.T) *metadb.DB {
	t.Helper()
	db, err := metadb.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}
func boundaryGate(t *testing.T) chan struct{} {
	gate := make(chan struct{})
	t.Cleanup(func() { closeBoundaryGate(gate) })
	return gate
}
func closeBoundaryGate(gate chan struct{}) {
	select {
	case <-gate:
	default:
		close(gate)
	}
}
func raiseBoundaryPeak(peak *atomic.Int64, n int64) {
	for old := peak.Load(); n > old; old = peak.Load() {
		if peak.CompareAndSwap(old, n) {
			return
		}
	}
}
func awaitBoundaryEvent(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("expected concurrent work did not enter")
	}
}
func assertBoundaryNoExtra(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
		t.Fatal("exceeded four concurrent workers")
	case <-time.After(30 * time.Millisecond):
	}
}
func startBoundaryRead(t *testing.T, s *Store, reads []PermissionMetadataRead) <-chan []PermissionMetadataReadResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	result := make(chan []PermissionMetadataReadResult, 1)
	joined := make(chan struct{})
	go func() { defer close(joined); result <- s.ReadSendPermissionMetadataBatch(ctx, reads) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
			t.Error("permission read did not join")
		}
	})
	return result
}
func awaitBoundaryResult(t *testing.T, result <-chan []PermissionMetadataReadResult) []PermissionMetadataReadResult {
	t.Helper()
	select {
	case out := <-result:
		return out
	case <-time.After(10 * time.Second):
		t.Fatal("permission read timed out")
		return nil
	}
}
