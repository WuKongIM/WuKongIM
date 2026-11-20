//go:build integration

package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// permissionReplyGate forwards the public RPC port to real Nodes. It delays
// only one actual successful reply; it never manufactures a route or fact.
type permissionReplyGate struct {
	proxy.Cluster
	node                        *cluster.Node
	slow                        uint64
	entered, fast, release      chan struct{}
	once, fastOnce, releaseOnce sync.Once
	expired                     atomic.Bool
	fastReplies                 atomic.Int64
}

func (g *permissionReplyGate) SendPermissionRoutes(keys []string) []proxy.SendPermissionRoute {
	return g.node.SendPermissionRoutes(keys)
}
func (g *permissionReplyGate) unblock() { g.releaseOnce.Do(func() { close(g.release) }) }
func (g *permissionReplyGate) RPCService(ctx context.Context, node multiraft.NodeID, slot multiraft.SlotID, service uint8, payload []byte) ([]byte, error) {
	raw, err := g.Cluster.RPCService(ctx, node, slot, service, payload)
	if err != nil {
		return raw, err
	}
	if uint64(node) != g.slow {
		g.fastReplies.Add(1)
		g.fastOnce.Do(func() { close(g.fast) })
		return raw, nil
	}
	g.once.Do(func() {
		close(g.entered)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-g.release:
		case <-timer.C:
			g.expired.Store(true)
		}
	})
	return raw, nil
}

// permissionCohortWitness observes actual collection and credits without
// blocking instrumentation or changing the product's five-millisecond window.
type permissionCohortWitness struct {
	cohorts, members, calls, bytes, owners atomic.Int64
}

func (o *permissionCohortWitness) ObserveSendPermissionCount(kind string, n int) {
	if kind == "cohorts" {
		o.cohorts.Add(int64(n))
	}
	if kind == "cohort_requests" {
		o.members.Add(int64(n))
	}
}
func (*permissionCohortWitness) ObserveSendPermissionStage(string, string, time.Duration) {}
func (*permissionCohortWitness) ObserveSendPermissionInflight(int)                        {}
func (o *permissionCohortWitness) ObserveSendPermissionCohortOwned(kind string, delta int) {
	switch kind {
	case "calls":
		o.calls.Add(int64(delta))
	case "budget_bytes":
		o.bytes.Add(int64(delta))
	case "cohorts":
		o.owners.Add(int64(delta))
	}
}

// Failure inventory: setup, route changes, split collection, RPC/fact errors,
// control expiry, artifact errors or incomplete cleanup invalidate the sample.
// Only a fully proven independent caller waiting for the held sibling is RED.
func TestSendPermissionIndependentCallerCompletesBeforeSlowSibling(t *testing.T) {
	runIndependentPermissionRead(t, "independent")
}

func TestSendPermissionIndependentCallerKeepsMixedMemberComplete(t *testing.T) {
	runIndependentPermissionRead(t, "mixed")
}

func TestSendPermissionIndependentCallerCancellationJoinsPhysicalWork(t *testing.T) {
	runIndependentPermissionRead(t, "cancel")
}

func TestSendPermissionIndependentCallerCloseJoinsPhysicalWork(t *testing.T) {
	runIndependentPermissionRead(t, "close")
}

func TestSendPermissionIndependentCallerDoesNotReuseCompletedFacts(t *testing.T) {
	runIndependentPermissionRead(t, "fresh")
}

func runIndependentPermissionRead(t *testing.T, scenario string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	var nodes []*cluster.Node
	var store *proxy.Store
	var db *metadb.DB
	var callers sync.WaitGroup
	gate := &permissionReplyGate{entered: make(chan struct{}), fast: make(chan struct{}), release: make(chan struct{})}
	witness := &permissionCohortWitness{}
	complete, before := false, false
	report := map[string]any{"hash_slots": 256, "nodes": 3, "replicas": 3, "control_max_ms": 2000, "observation_ms": 250, "scenario": scenario}
	t.Cleanup(func() {
		gate.unblock()
		cancel()
		callers.Wait()
		if store != nil {
			store.CloseSendPermissionReads()
		}
		stop, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		joined := true
		for _, n := range nodes {
			joined = n.Stop(stop) == nil && joined
		}
		if db != nil {
			joined = db.Close() == nil && joined
		}
		zero := witness.calls.Load() == 0 && witness.bytes.Load() == 0 && witness.owners.Load() == 0
		valid := complete && joined && zero && !gate.expired.Load()
		report["checks_complete"], report["cleanup_joined"], report["credits_zero"] = complete, joined, zero
		report["control_expired"], report["independent_before_release"] = gate.expired.Load(), before
		report["valid"], report["behavior_red"], report["passed"] = valid, valid && !before, valid && !t.Failed()
		dir := os.Getenv("WK_PERMISSION_TEST_REPORT_DIR")
		if dir == "" {
			dir = t.TempDir()
		}
		require.NoError(t, os.MkdirAll(dir, 0700))
		raw, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		final := filepath.Join(dir, t.Name()+".json")
		partial := final + ".partial"
		f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		require.NoError(t, err)
		_, err = f.Write(append(raw, '\n'))
		ce := f.Close()
		require.NoError(t, errors.Join(err, ce))
		require.NoError(t, os.Link(partial, final))
		require.NoError(t, os.Remove(partial))
		require.True(t, joined && zero, "invalid cleanup")
	})
	voters := make([]cluster.ControlVoter, 3)
	for i := range voters {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: ln.Addr().String()}
		require.NoError(t, ln.Close())
	}
	for _, v := range voters {
		n, err := cluster.New(cluster.Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: cluster.ControlConfig{ClusterID: "permission-independent-public", Voters: voters, AllowBootstrap: true}, Slots: cluster.SlotConfig{InitialSlotCount: 12, HashSlotCount: 256, ReplicaCount: 3}, Channel: cluster.ChannelConfig{ReplicaCount: 3}})
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	starts := make(chan error, 3)
	for _, n := range nodes {
		callers.Add(1)
		go func() { defer callers.Done(); starts <- n.Start(ctx) }()
	}
	for range nodes {
		require.NoError(t, <-starts)
	}
	require.Eventually(t, func() bool {
		for _, n := range nodes {
			if n.ProbeWriteReady(ctx) != nil {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond)
	var keys [2]string
	require.Eventually(t, func() bool {
		for i := 0; i < 512; i++ {
			key := fmt.Sprintf("permission-independent-%d", i)
			r := nodes[0].SendPermissionRoutes([]string{key})
			if len(r) != 1 || r[0].Err != nil {
				continue
			}
			if owner := r[0].Fence.LeaderNodeID; owner == 2 || owner == 3 {
				keys[owner-2] = key
			}
			if keys[0] != "" && keys[1] != "" {
				return true
			}
		}
		return false
	}, 20*time.Second, 20*time.Millisecond)
	for _, key := range keys {
		require.NoError(t, nodes[0].CreateUserMetadata(ctx, metadb.User{UID: key}))
	}
	_, banErr := nodes[0].SetSendBanMetadata(ctx, metadb.SendBanMutation{UID: keys[0], SendBan: 1})
	require.NoError(t, banErr)
	var err error
	db, err = metadb.Open(filepath.Join(t.TempDir(), "origin-unused"))
	require.NoError(t, err)
	gate.Cluster, gate.node, gate.slow = nodes[0], nodes[0], 2
	store = proxy.New(gate, db)
	store.SetSendPermissionObserver(witness)
	start := make(chan struct{})
	count := 2
	if scenario == "mixed" {
		count = 3
	}
	waits := make([]chan []proxy.PermissionMetadataReadResult, count)
	slowContext, cancelSlow := context.WithCancel(ctx)
	defer cancelSlow()
	for i := range waits {
		key := keys[min(i, 1)]
		waits[i] = make(chan []proxy.PermissionMetadataReadResult, 1)
		read := proxy.PermissionMetadataRead{Kind: proxy.PermissionMetadataReadUserSendPolicy, UID: key}
		reads := []proxy.PermissionMetadataRead{read, read, read}
		callContext := ctx
		if i == 0 {
			callContext = slowContext
		}
		if i == 2 {
			reads[0].UID = keys[0]
		}
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			waits[i] <- store.ReadSendPermissionMetadataBatch(callContext, reads)
		}()
	}
	close(start)
	for _, event := range []<-chan struct{}{gate.entered, gate.fast} {
		select {
		case <-event:
		case <-ctx.Done():
			t.Fatal("invalid RPC control entry")
		}
	}
	require.EqualValues(t, 1, witness.cohorts.Load(), "invalid split cohort")
	require.EqualValues(t, count, witness.members.Load(), "invalid member witness")
	var fast []proxy.PermissionMetadataReadResult
	timer := time.NewTimer(250 * time.Millisecond)
	select {
	case fast = <-waits[1]:
		before = true
	case <-timer.C:
	}
	timer.Stop()
	var closed chan struct{}
	if before {
		// Returning one member must not refund inputs still borrowed by the
		// held physical execution or admit a completed result to a late caller.
		require.EqualValues(t, count, witness.calls.Load())
		require.EqualValues(t, 1, witness.owners.Load())
		switch scenario {
		case "mixed":
			select {
			case <-waits[2]:
				t.Fatal("mixed member published a partial result")
			default:
			}
		case "cancel":
			cancelSlow()
			select {
			case <-waits[0]:
				t.Fatal("last cancellation escaped physical join")
			case <-time.After(50 * time.Millisecond):
			}
		case "close":
			closed = make(chan struct{})
			callers.Add(1)
			go func() { defer callers.Done(); store.CloseSendPermissionReads(); close(closed) }()
			select {
			case <-closed:
				t.Fatal("Close escaped physical join")
			case <-time.After(50 * time.Millisecond):
			}
		case "fresh":
			_, mutationErr := nodes[0].SetSendBanMetadata(ctx, metadb.SendBanMutation{UID: keys[1], SendBan: 1})
			require.NoError(t, mutationErr)
			prior := gate.fastReplies.Load()
			fresh := store.ReadSendPermissionMetadataBatch(ctx, []proxy.PermissionMetadataRead{{Kind: proxy.PermissionMetadataReadUserSendPolicy, UID: keys[1]}})
			require.Len(t, fresh, 1)
			require.NoError(t, fresh[0].Err)
			require.True(t, fresh[0].Found)
			require.EqualValues(t, 1, fresh[0].UserPolicy.SendBan)
			require.Greater(t, gate.fastReplies.Load(), prior, "late caller reused completed facts")
		}
	}
	gate.unblock()
	if fast == nil {
		select {
		case fast = <-waits[1]:
		case <-ctx.Done():
			t.Fatal("fast caller did not join")
		}
	}
	var slow []proxy.PermissionMetadataReadResult
	select {
	case slow = <-waits[0]:
	case <-ctx.Done():
		t.Fatal("slow caller did not join")
	}
	checkFacts := func(results []proxy.PermissionMetadataReadResult, bans ...int) {
		require.Len(t, results, 3)
		for i, r := range results {
			require.NoError(t, r.Err)
			require.True(t, r.Found)
			require.EqualValues(t, bans[i], r.UserPolicy.SendBan)
		}
	}
	checkFacts(fast, 0, 0, 0)
	if scenario == "cancel" {
		for _, r := range slow {
			require.ErrorIs(t, r.Err, context.Canceled)
		}
	} else if scenario != "close" {
		checkFacts(slow, 1, 1, 1)
	}
	if scenario == "mixed" {
		select {
		case mixed := <-waits[2]:
			checkFacts(mixed, 1, 0, 0)
		case <-ctx.Done():
			t.Fatal("mixed caller did not join")
		}
	}
	if closed != nil {
		select {
		case <-closed:
		case <-ctx.Done():
			t.Fatal("Close did not join")
		}
	}
	complete = true
	require.False(t, gate.expired.Load(), "invalid expired control")
	require.True(t, before, "independent completed facts waited for an unrelated RPC reply")
}
