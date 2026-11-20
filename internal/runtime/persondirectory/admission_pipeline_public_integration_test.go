//go:build integration

package persondirectory_test

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

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/persondirectory"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/worker"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// admissionReturnControl delays publication through the synchronous public
// append-stage observer. All default Node runtimes and actual Raft remain intact.
type admissionReturnControl struct {
	armed   atomic.Bool
	expired atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*admissionReturnControl) SetReactorMailboxDepth(int, string, int)                   {}
func (*admissionReturnControl) SetWorkerQueueDepth(string, int)                           {}
func (*admissionReturnControl) ObserveAppendBatch(int, int, time.Duration)                {}
func (*admissionReturnControl) ObserveAppendLatency(ch.CommitMode, time.Duration)         {}
func (*admissionReturnControl) ObserveWorkerResult(worker.TaskKind, error, time.Duration) {}
func (o *admissionReturnControl) unblock()                                                { o.once.Do(func() { close(o.release) }) }

// admissionReturnObserver holds only the publication of a real successful
// source admission. Raft has completed and other source leaders remain free.
type admissionReturnObserver struct {
	admissionReturnControl
	held atomic.Bool
}

func (o *admissionReturnObserver) ObserveChannelAppendStage(stage, result string, _ time.Duration) {
	if stage != "meta_create_slot_propose_wait" || result != "ok" || !o.armed.CompareAndSwap(true, false) {
		return
	}
	o.held.Store(true)
	close(o.entered)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-o.release:
	case <-timer.C:
		o.expired.Store(true)
	}
}

// Failure inventory: missing independent source or control entry, control
// expiry, failed fresh durable proof and incomplete joins invalidate evidence.
// A valid independent Ready result must not wait for another source admission.
func TestDirectoryPreparationIndependentSourceAdmission(t *testing.T) {
	testDirectoryAdmissionPipeline(t, false)
}

func TestDirectoryPreparationCanceledAdmissionJoins(t *testing.T) {
	testDirectoryAdmissionPipeline(t, true)
}

func testDirectoryAdmissionPipeline(t *testing.T, cancelHeld bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	report := map[string]any{"scenario": t.Name(), "hash_slots": 256, "physical_slots": 12, "nodes": 3}
	voters := make([]cluster.ControlVoter, 3)
	for i := range voters {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		voters[i] = cluster.ControlVoter{NodeID: uint64(i + 1), Addr: l.Addr().String()}
		require.NoError(t, l.Close())
	}
	o := &admissionReturnObserver{admissionReturnControl: admissionReturnControl{entered: make(chan struct{}), release: make(chan struct{})}}
	var nodes []*cluster.Node
	var projectors []*persondirectory.Projector
	var callers sync.WaitGroup
	var executionCalls [3]atomic.Int32
	t.Cleanup(func() {
		o.unblock()
		cancel()
		callers.Wait()
		stop, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		joined := true
		for _, p := range projectors {
			joined = p.Stop(stop) == nil && joined
		}
		for _, n := range nodes {
			joined = n.Stop(stop) == nil && joined
		}
		valid := joined && !o.expired.Load() && report["checks_complete"] == true
		report["cleanup_joined"] = joined
		report["control_expired"] = o.expired.Load()
		report["valid"] = valid
		report["behavior_red"] = valid && report["independent_result_before_release"] == false
		report["passed"] = valid && !t.Failed()
		dir := os.Getenv("WK_DIRECTORY_TEST_REPORT_DIR")
		if dir == "" {
			dir = t.TempDir()
		}
		require.NoError(t, os.MkdirAll(dir, 0700))
		b, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		target := filepath.Join(dir, t.Name()+".json")
		partial := target + ".partial"
		f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, err = f.Write(append(b, '\n'))
		require.NoError(t, errors.Join(err, f.Close()))
		require.NoError(t, os.Link(partial, target))
		require.True(t, valid, "invalid setup/control/cleanup evidence")
	})
	for _, v := range voters {
		cfg := cluster.Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(),
			Control: cluster.ControlConfig{ClusterID: "directory-admission-pipeline", Voters: voters, AllowBootstrap: true},
			Slots:   cluster.SlotConfig{InitialSlotCount: 12, HashSlotCount: 256, ReplicaCount: 3}, Channel: cluster.ChannelConfig{ReplicaCount: 3}}
		if v.NodeID == 1 {
			cfg.Channel.Observer = o
		}
		n, err := cluster.New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
		p, err := persondirectory.New(persondirectory.Options{Source: n, Memberships: clusterinfra.NewPersonDirectoryMembershipWriter(n)})
		require.NoError(t, err)
		projectors = append(projectors, p)
		n.SetPersonDirectoryPreparation(func(ctx context.Context, entries []metadb.PersonDirectoryTaskEntry) []error {
			executionCalls[v.NodeID-1].Add(1)
			return p.Prepare(ctx, entries)
		})
	}
	started := make(chan error, 3)
	for _, n := range nodes {
		go func() { started <- n.Start(ctx) }()
	}
	for range nodes {
		require.NoError(t, <-started)
	}
	require.Eventually(t, func() bool {
		for _, n := range nodes {
			if n.ProbeWriteReady(ctx) != nil {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond)
	for _, p := range projectors {
		require.NoError(t, p.Start(ctx))
	}
	// Choose actual stable source leaders through the public router, never by
	// assigning fabricated Slot authority or overriding the real proposer.
	var tasks [2]metadb.PersonDirectoryTask
	require.Eventually(t, func() bool {
		for i := 0; i < 512; i++ {
			task := metadb.PersonDirectoryTask{ChannelID: channelid.EncodePersonChannel(fmt.Sprintf("pipeline-left-%d", i), fmt.Sprintf("pipeline-right-%d", i)), ChannelType: 1, CreatedAt: 1}
			routes, err := nodes[0].RouteKeysPartial([]string{task.ChannelID})
			if err != nil || len(routes) != 1 || routes[0].Err != nil {
				continue
			}
			leader := routes[0].Route.Leader
			if leader == 1 {
				tasks[0] = task
			} else if leader > 1 {
				tasks[1] = task
			}
			if tasks[0].ChannelID != "" && tasks[1].ChannelID != "" {
				return true
			}
		}
		return false
	}, 20*time.Second, 20*time.Millisecond)
	report["independent_sources_confirmed"] = true
	type outcome struct {
		index int
		fence metadb.PersonDirectoryAppendFence
		err   error
	}
	outcomes := make(chan outcome, 2)
	joined := make(chan struct{})
	operation, cancelOperation := context.WithCancel(ctx)
	defer cancelOperation()
	o.armed.Store(true)
	callers.Add(1)
	go func() {
		defer callers.Done()
		defer close(joined)
		nodes[0].PreparePersonDirectoryTaskWaves(operation, tasks[:], func(i int, f metadb.PersonDirectoryAppendFence, err error) { outcomes <- outcome{i, f, err} })
	}()
	select {
	case <-o.entered:
		report["control_entered"] = true
	case <-ctx.Done():
		t.Fatal("invalid: control not entered")
	}
	var results []outcome
	independent := false
	timer := time.NewTimer(900 * time.Millisecond)
	select {
	case r := <-outcomes:
		results = append(results, r)
		independent = r.index == 1 && r.err == nil && r.fence.Valid()
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal("invalid: context canceled")
	}
	timer.Stop()
	report["independent_result_before_release"] = independent
	if cancelHeld {
		cancelOperation()
		select {
		case <-joined:
			t.Fatal("accepted admission owner returned before held call joined")
		case <-time.After(50 * time.Millisecond):
		}
		report["canceled_owner_remained_joined"] = true
	}
	o.unblock()
	for len(results) < 2 {
		select {
		case r := <-outcomes:
			results = append(results, r)
		case <-ctx.Done():
			t.Fatal("invalid: result did not join")
		}
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("invalid: preparation owner did not join")
	}
	counts := [2]int{}
	reads := make([]slotproxy.PermissionMetadataRead, 2)
	for i, task := range tasks {
		reads[i] = slotproxy.PermissionMetadataRead{Kind: slotproxy.PermissionMetadataReadChannel, ChannelID: task.ChannelID, ChannelType: 1}
	}
	facts := nodes[0].ReadSendPermissionMetadataBatch(ctx, reads)
	require.Len(t, facts, 2)
	for _, r := range results {
		require.GreaterOrEqual(t, r.index, 0)
		require.Less(t, r.index, 2)
		counts[r.index]++
		if cancelHeld && r.index == 0 {
			require.ErrorIs(t, r.err, context.Canceled)
			require.False(t, r.fence.Valid())
			continue
		}
		require.NoError(t, r.err)
		require.True(t, r.fence.Valid())
		require.NoError(t, facts[r.index].Err)
		require.True(t, facts[r.index].Found)
		require.Equal(t, metadb.DirectoryProjectionReady, facts[r.index].Channel.DirectoryProjectionState)
		require.Equal(t, facts[r.index].PersonDirectoryFence, r.fence)
	}
	require.Equal(t, [2]int{1, 1}, counts)
	expectedCalls := int32(2)
	if cancelHeld {
		expectedCalls = 1
	}
	calls := int32(0)
	for i := range executionCalls {
		calls += executionCalls[i].Load()
	}
	require.Equal(t, expectedCalls, calls, "source-node grouping must not add RPC executions")
	report["preparation_executions"] = calls
	report["fresh_ready_fences_verified"] = expectedCalls
	report["checks_complete"] = true
	require.True(t, independent, "independent fresh Ready result waited for unrelated source admission")
}
