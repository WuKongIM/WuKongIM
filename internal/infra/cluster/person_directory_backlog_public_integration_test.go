//go:build integration

package cluster_test

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
	message "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	registry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// backlogReturnGate keeps actual completed public Node calls owned until the
// fixture releases them. It never manufactures a directory or runtime proof.
type backlogReturnGate struct {
	*cluster.Node
	mu                  sync.Mutex
	next, active, peak  int
	sizes               []int
	entered             chan int
	first, rest         chan struct{}
	firstOnce, restOnce sync.Once
	expired             atomic.Bool
}

func (g *backlogReturnGate) releaseFirst() { g.firstOnce.Do(func() { close(g.first) }) }
func (g *backlogReturnGate) releaseRest()  { g.restOnce.Do(func() { close(g.rest) }) }

func (g *backlogReturnGate) PreparePersonDirectoryTaskWaves(ctx context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, metadb.PersonDirectoryAppendFence, error)) {
	g.mu.Lock()
	id := g.next
	g.next++
	g.active++
	g.peak = max(g.peak, g.active)
	g.sizes = append(g.sizes, len(tasks))
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
	type result struct {
		index int
		fence metadb.PersonDirectoryAppendFence
		err   error
	}
	var results []result
	g.Node.PreparePersonDirectoryTaskWaves(ctx, tasks, func(i int, f metadb.PersonDirectoryAppendFence, err error) {
		results = append(results, result{i, f, err})
	})
	g.entered <- id
	if id != 8 {
		release := g.rest
		if id == 0 {
			release = g.first
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-release:
		case <-timer.C:
			g.expired.Store(true)
		}
		timer.Stop()
	}
	for _, r := range results {
		emit(r.index, r.fence, r.err)
	}
}

// Failure inventory: setup, missing public ownership checkpoint, expired
// controls, invalid/duplicate results, missing fresh fences, artifact failure
// and incomplete cleanup invalidate evidence rather than establishing RED.
func TestPersonDirectoryQueuedPrefixUsesAvailableExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	report := map[string]any{"nodes": 1, "hash_slots": 256, "physical_slots": 12, "queued_prefix": 128}
	g := &backlogReturnGate{entered: make(chan int, 32), first: make(chan struct{}), rest: make(chan struct{})}
	reg := registry.New()
	var p *persondirectory.Projector
	var store *clusterinfra.ChannelMetadataStore
	var callers sync.WaitGroup
	t.Cleanup(func() {
		g.releaseFirst()
		g.releaseRest()
		cancel()
		callers.Wait()
		stop, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		joined := true
		if store != nil {
			joined = store.Stop(stop) == nil && joined
		}
		if p != nil {
			joined = p.Stop(stop) == nil && joined
		}
		if g.Node != nil {
			joined = g.Node.Stop(stop) == nil && joined
		}
		joined = joined && reg.Snapshot().ManagedTotal == 0
		g.mu.Lock()
		report["physical_sizes"] = append([]int(nil), g.sizes...)
		report["peak_active"] = g.peak
		g.mu.Unlock()
		valid := joined && !g.expired.Load() && report["checks_complete"] == true
		report["cleanup_joined"] = joined
		report["control_expired"] = g.expired.Load()
		report["valid"] = valid
		report["behavior_red"] = valid && report["prefix_before_release"] != 128
		report["passed"] = valid && !t.Failed()
		dir := os.Getenv("WK_DIRECTORY_TEST_REPORT_DIR")
		if dir == "" {
			dir = t.TempDir()
		}
		require.NoError(t, os.MkdirAll(dir, 0700))
		b, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		target := filepath.Join(dir, t.Name()+".json")
		f, err := os.OpenFile(target+".partial", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, err = f.Write(append(b, '\n'))
		require.NoError(t, errors.Join(err, f.Close()))
		require.NoError(t, os.Link(target+".partial", target))
		require.True(t, valid, "invalid fixture or cleanup")
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	g.Node, err = cluster.New(cluster.Config{NodeID: 1, ListenAddr: addr, DataDir: t.TempDir(), Control: cluster.ControlConfig{ClusterID: "directory-backlog", AllowBootstrap: true, Voters: []cluster.ControlVoter{{NodeID: 1, Addr: addr}}}, Slots: cluster.SlotConfig{InitialSlotCount: 12, HashSlotCount: 256, ReplicaCount: 1}, Channel: cluster.ChannelConfig{ReplicaCount: 1}})
	require.NoError(t, err)
	p, err = persondirectory.New(persondirectory.Options{Source: g.Node, Memberships: clusterinfra.NewPersonDirectoryMembershipWriter(g.Node)})
	require.NoError(t, err)
	g.Node.SetPersonDirectoryPreparation(p.Prepare)
	require.NoError(t, g.Node.Start(ctx))
	require.Eventually(t, func() bool { return g.Node.ProbeWriteReady(ctx) == nil }, 20*time.Second, 10*time.Millisecond)
	require.NoError(t, p.Start(ctx))
	store = clusterinfra.NewChannelMetadataStore(g, nil, reg)
	const count = 8 + 129
	admissions := make([]message.PersonDirectoryAdmission, count)
	for i := range admissions {
		admissions[i] = message.PersonDirectoryAdmission{Context: ctx, ChannelID: channelid.EncodePersonChannel(fmt.Sprintf("backlog-left-%d", i), fmt.Sprintf("backlog-right-%d", i)), ChannelType: 1}
	}
	type outcome struct {
		index int
		value message.PersonDirectoryAdmissionOutcome
	}
	results := make(chan outcome, count)
	start := func(from, to int) {
		callers.Add(1)
		go func() {
			defer callers.Done()
			store.AdmitPersonChannelDirectoryWaves(admissions[from:to], func(wave []message.PersonDirectoryAdmissionOutcome) {
				for _, v := range wave {
					results <- outcome{from + v.Index, v}
				}
			})
		}()
	}
	for i := 0; i < 8; i++ {
		start(i, i+1)
		select {
		case id := <-g.entered:
			require.Equal(t, i, id)
		case <-ctx.Done():
			t.Fatal("invalid initial controls")
		}
		require.False(t, g.expired.Load())
	}
	start(8, count)
	// Public task ownership is only a fixture checkpoint for this frozen
	// batching implementation: the fifth queued owner starts after four full
	// 32-item batches have been sealed. No private queue/entry state is read.
	require.Eventually(t, func() bool {
		for _, module := range reg.Snapshot().Modules {
			for _, task := range module.Tasks {
				if task.Task == registry.TaskMessageDirectoryBatch && task.Active == 13 {
					return true
				}
			}
		}
		return false
	}, 500*time.Millisecond, time.Millisecond)
	report["queued_prefix_checkpoint"] = true
	g.releaseFirst()
	seen := make(map[int]metadb.PersonDirectoryAppendFence)
	accept := func(r outcome) {
		require.GreaterOrEqual(t, r.index, 0)
		require.Less(t, r.index, count)
		_, duplicate := seen[r.index]
		require.False(t, duplicate)
		require.NoError(t, r.value.Err)
		require.True(t, r.value.PersonDirectoryFence.Valid())
		seen[r.index] = r.value.PersonDirectoryFence
	}
	timer := time.NewTimer(900 * time.Millisecond)
	prefix := 0
	for prefix < 128 {
		select {
		case r := <-results:
			accept(r)
			if r.index >= 8 && r.index < 136 {
				prefix++
			}
		case <-timer.C:
			goto observed
		case <-ctx.Done():
			t.Fatal("invalid context")
		}
	}
observed:
	timer.Stop()
	report["prefix_before_release"] = prefix
	g.releaseRest()
	for len(seen) < count {
		select {
		case r := <-results:
			accept(r)
		case <-ctx.Done():
			t.Fatal("invalid missing final results")
		}
	}
	callers.Wait()
	reads := make([]slotproxy.PermissionMetadataRead, count)
	for i, a := range admissions {
		reads[i] = slotproxy.PermissionMetadataRead{Kind: slotproxy.PermissionMetadataReadChannel, ChannelID: a.ChannelID, ChannelType: 1}
	}
	facts := g.Node.ReadSendPermissionMetadataBatch(ctx, reads)
	require.Len(t, facts, count)
	for i, r := range facts {
		require.NoError(t, r.Err)
		require.True(t, r.Found)
		require.Equal(t, seen[i], r.PersonDirectoryFence)
	}
	g.mu.Lock()
	peak := g.peak
	sizes := append([]int(nil), g.sizes...)
	g.mu.Unlock()
	require.LessOrEqual(t, peak, 8)
	for _, size := range sizes {
		require.LessOrEqual(t, size, 128)
	}
	report["verified_fences"] = count
	report["checks_complete"] = true
	require.Equal(t, 128, prefix, "already queued prefix waited for another execution turn")
}
