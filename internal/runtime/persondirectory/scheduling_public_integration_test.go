//go:build integration

package persondirectory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	registry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/persondirectory"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

// scanReturnControl preserves real storage results. Completion waits only for
// the controlled scan to enter, establishing the order without a storage fake.
// Optional return gates exercise caller cancellation and Stop ownership.
type scanReturnControl struct {
	*cluster.Node
	projector                                  *persondirectory.Projector
	armed                                      atomic.Bool
	once, initialOnce                          sync.Once
	entered, release, durable, initialDone     chan struct{}
	expired                                    atomic.Bool
	failScan                                   atomic.Bool
	failedScans                                atomic.Int32
	holdCompletion, holdCanceledScan, disabled bool
}

func (s *scanReturnControl) wait(ctx context.Context, release <-chan struct{}) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-release:
	case <-ctx.Done():
	case <-timer.C:
		s.expired.Store(true)
	}
}

func (s *scanReturnControl) ListPersonDirectoryTaskPage(ctx context.Context, slot metadb.HashSlot, after metadb.PersonDirectoryTaskCursor, limit int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	rows, next, done, err := s.Node.ListPersonDirectoryTaskPage(ctx, slot, after, limit)
	if err == nil && slot == 255 {
		s.initialOnce.Do(func() { close(s.initialDone) })
	}
	if err == nil && s.failScan.Load() {
		s.failedScans.Add(1)
		return nil, after, false, context.DeadlineExceeded
	}
	if err == nil && s.armed.Load() {
		s.once.Do(func() {
			close(s.entered)
			if s.holdCanceledScan {
				ctx = context.Background()
			}
			s.wait(ctx, s.release)
		})
	}
	return rows, next, done, err
}

func (s *scanReturnControl) CompletePersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	errs := s.Node.CompletePersonDirectoryTasks(ctx, tasks)
	if !s.disabled && len(errs) == len(tasks) && errors.Join(errs...) == nil && s.armed.CompareAndSwap(false, true) {
		close(s.durable)
		s.projector.Wake()
		s.wait(ctx, s.entered)
		if s.holdCompletion {
			s.wait(ctx, s.release)
		}
	}
	return errs
}

type schedulingFixture struct {
	ctx       context.Context
	node      *cluster.Node
	source    *scanReturnControl
	projector *persondirectory.Projector
	registry  *registry.Registry
	evidence  map[string]any
	release   func()
	callers   sync.WaitGroup
}

func newSchedulingFixture(t *testing.T, mode string) *schedulingFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	f := &schedulingFixture{ctx: ctx, evidence: map[string]any{"nodes": 1, "hash_slots": 256, "control_complete": false, "behavior_red": false}}
	dir := os.Getenv("WK_PERSON_DIRECTORY_TEST_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(dir, 0700))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	n, err := cluster.New(cluster.Config{NodeID: 1, ListenAddr: addr, DataDir: t.TempDir(),
		Control: cluster.ControlConfig{ClusterID: "directory-scheduling-public", AllowBootstrap: true, Voters: []cluster.ControlVoter{{NodeID: 1, Addr: addr}}},
		Slots:   cluster.SlotConfig{InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1}})
	require.NoError(t, err)
	f.node = n
	s := &scanReturnControl{Node: n, entered: make(chan struct{}), release: make(chan struct{}), durable: make(chan struct{}), initialDone: make(chan struct{}),
		holdCompletion: mode == "cancel", holdCanceledScan: mode == "stop", disabled: mode == "fairness" || mode == "scan-error"}
	s.failScan.Store(mode == "scan-error")
	f.registry = registry.New()
	p, err := persondirectory.New(persondirectory.Options{Goroutines: f.registry, Source: s, Memberships: clusterinfra.NewPersonDirectoryMembershipWriter(n)})
	require.NoError(t, err)
	s.projector = p
	f.source = s
	f.projector = p
	var once sync.Once
	f.release = func() { once.Do(func() { close(s.release) }) }
	t.Cleanup(func() {
		f.release()
		cancel()
		f.callers.Wait()
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		joined := p.Stop(stop) == nil
		joined = n.Stop(stop) == nil && joined
		f.evidence["cleanup_joined"] = joined
		f.evidence["control_expired"] = s.expired.Load()
		valid := joined && !s.expired.Load() && f.evidence["control_complete"] == true
		f.evidence["valid"] = valid
		f.evidence["behavior_red"] = valid && f.evidence["blocked_completion"] == true
		f.evidence["passed"] = valid && !t.Failed()
		data, e := json.MarshalIndent(f.evidence, "", "  ")
		require.NoError(t, e)
		file, e := os.OpenFile(filepath.Join(dir, t.Name()+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, e)
		_, e = file.Write(append(data, '\n'))
		require.NoError(t, errors.Join(e, file.Close()))
		require.True(t, joined, "invalid cleanup")
	})
	require.NoError(t, n.Start(ctx))
	require.Eventually(t, func() bool { return n.ProbeWriteReady(ctx) == nil }, 20*time.Second, 20*time.Millisecond)
	require.NoError(t, p.Start(ctx))
	select {
	case <-s.initialDone:
	case <-ctx.Done():
		t.Fatal("invalid initial discovery")
	}
	return f
}

func (f *schedulingFixture) seed(t *testing.T, index int) metadb.PersonDirectoryTaskEntry {
	t.Helper()
	id := channelid.EncodePersonChannel(fmt.Sprintf("left-%d", index), fmt.Sprintf("right-%d", index))
	errs := f.node.AdmitPersonDirectoryTasks(f.ctx, []metadb.PersonDirectoryTask{{ChannelID: id, ChannelType: 1, CreatedAt: 1}})
	require.Len(t, errs, 1)
	require.NoError(t, errs[0])
	route, err := f.node.RouteKey(id)
	require.NoError(t, err)
	rows, _, _, err := f.node.ListPersonDirectoryTaskPage(f.ctx, metadb.HashSlot(route.HashSlot), metadb.PersonDirectoryTaskCursor{}, 64)
	require.NoError(t, err)
	for _, row := range rows {
		if row.ChannelID == id {
			return metadb.PersonDirectoryTaskEntry{HashSlot: metadb.HashSlot(route.HashSlot), Task: row}
		}
	}
	t.Fatal("invalid fixture: pending task disappeared before admission")
	return metadb.PersonDirectoryTaskEntry{}
}

func (f *schedulingFixture) prepare(ctx context.Context, item metadb.PersonDirectoryTaskEntry) <-chan []error {
	done := make(chan []error, 1)
	f.callers.Add(1)
	go func() {
		defer f.callers.Done()
		done <- f.projector.Prepare(ctx, []metadb.PersonDirectoryTaskEntry{item})
	}()
	return done
}

func (f *schedulingFixture) controlled(t *testing.T, item metadb.PersonDirectoryTaskEntry) {
	t.Helper()
	select {
	case <-f.source.entered:
	case <-f.ctx.Done():
		t.Fatal("invalid control entry")
	}
	select {
	case <-f.source.durable:
	default:
		t.Fatal("invalid durable completion")
	}
	meta, err := f.node.GetChannelMetadataAuthoritative(f.ctx, item.Task.ChannelID, 1)
	require.NoError(t, err)
	require.Equal(t, metadb.DirectoryProjectionReady, meta.DirectoryProjectionState)
	f.evidence["control_complete"] = true
}

func awaitPreparation(t *testing.T, ctx context.Context, done <-chan []error) []error {
	t.Helper()
	select {
	case errs := <-done:
		require.Len(t, errs, 1)
		return errs
	case <-ctx.Done():
		t.Fatal("Prepare did not join")
		return nil
	}
}

// Failure inventory: a real completed Prepare may be trapped behind recovery
// I/O; setup/control/cleanup failures must never count as a product RED;
// releasing the scan must preserve the original durable result and join owners.
func TestPrepareCompletionIndependentOfRecoveryScan(t *testing.T) {
	f := newSchedulingFixture(t, "")
	item := f.seed(t, 0)
	done := f.prepare(f.ctx, item)
	f.controlled(t, item)
	select {
	case errs := <-done:
		require.Len(t, errs, 1)
		require.NoError(t, errs[0])
		f.evidence["blocked_completion"] = false
	case <-time.After(time.Second):
		f.evidence["blocked_completion"] = true
		t.Error("durable Prepare result blocked behind unrelated recovery scan")
		f.release()
		require.NoError(t, errors.Join(awaitPreparation(t, f.ctx, done)...))
	}
}

// Canceling one caller must not revoke durable work or a sibling's result.
func TestPrepareCancellationKeepsSharedCompletion(t *testing.T) {
	f := newSchedulingFixture(t, "cancel")
	item := f.seed(t, 0)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	first := f.prepare(ctx, item)
	f.controlled(t, item)
	second := f.prepare(f.ctx, item)
	cancel()
	require.ErrorIs(t, errors.Join(awaitPreparation(t, f.ctx, first)...), context.Canceled)
	f.release()
	require.NoError(t, errors.Join(awaitPreparation(t, f.ctx, second)...))
}

// Stop owns the actual outstanding read even when its returned value is late.
func TestProjectorStopJoinsRecoveryScan(t *testing.T) {
	f := newSchedulingFixture(t, "stop")
	item := f.seed(t, 0)
	done := f.prepare(f.ctx, item)
	f.controlled(t, item)
	require.NoError(t, errors.Join(awaitPreparation(t, f.ctx, done)...))
	short, cancel := context.WithTimeout(f.ctx, 50*time.Millisecond)
	require.ErrorIs(t, f.projector.Stop(short), context.DeadlineExceeded)
	cancel()
	f.release()
	require.NoError(t, f.projector.Stop(f.ctx))
}

// Recovery must progress before this bounded foreground request sequence ends;
// only authoritative Ready and both real UID memberships establish success.
func TestRecoveryProgressAlongsideDirectPreparation(t *testing.T) {
	f := newSchedulingFixture(t, "fairness")
	recovered := f.seed(t, 0)
	meta, err := f.node.GetChannelMetadataAuthoritative(f.ctx, recovered.Task.ChannelID, 1)
	require.NoError(t, err)
	require.Equal(t, metadb.DirectoryProjectionPending, meta.DirectoryProjectionState, "invalid fixture: recovery completed before foreground sequence")
	observedAt := 0
	for i := 1; i <= 16; i++ {
		item := f.seed(t, i)
		require.NoError(t, errors.Join(awaitPreparation(t, f.ctx, f.prepare(f.ctx, item))...))
		f.projector.Wake()
		if observedAt != 0 || i == 16 {
			continue
		}
		meta, err := f.node.GetChannelMetadataAuthoritative(f.ctx, recovered.Task.ChannelID, 1)
		require.NoError(t, err)
		if meta.DirectoryProjectionState != metadb.DirectoryProjectionReady {
			continue
		}
		for _, uid := range []string{"left-0", "right-0"} {
			rows, err := f.node.GetUserChannelMemberships(f.ctx, uid, []metadb.ChannelKey{{ChannelID: recovered.Task.ChannelID, ChannelType: 1}})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, recovered.Task.Generation, rows[0].SourceVersion)
		}
		observedAt = i
	}
	f.evidence["foreground_completed_at_recovery"] = observedAt
	f.evidence["foreground_remaining_at_recovery"] = 16 - observedAt
	f.evidence["foreground_total_completed"] = 16
	f.evidence["control_complete"] = true
	require.Greater(t, observedAt, 0, "recovery did not progress before foreground sequence ended")
}

// Failed discovery cannot manufacture Ready; the unchanged durable task must
// still be recoverable once reads become available again.
func TestRecoveryReadFailurePreservesPendingTask(t *testing.T) {
	f := newSchedulingFixture(t, "scan-error")
	item := f.seed(t, 0)
	before := f.source.failedScans.Load()
	f.projector.Wake()
	require.Eventually(t, func() bool { return f.source.failedScans.Load() >= before+256 }, time.Second, time.Millisecond)
	meta, err := f.node.GetChannelMetadataAuthoritative(f.ctx, item.Task.ChannelID, 1)
	require.NoError(t, err)
	require.Equal(t, metadb.DirectoryProjectionPending, meta.DirectoryProjectionState)
	f.source.failScan.Store(false)
	f.projector.Wake()
	require.Eventually(t, func() bool {
		m, e := f.node.GetChannelMetadataAuthoritative(f.ctx, item.Task.ChannelID, 1)
		return e == nil && m.DirectoryProjectionState == metadb.DirectoryProjectionReady
	}, 4*time.Second, 10*time.Millisecond)
	f.evidence["control_complete"] = true
}

// Failure inventory: independent permanent owners must not overdeclare a
// singleton, and all owned loops must retire when public Stop returns.
func TestProjectorRegistryOwners(t *testing.T) {
	f := newSchedulingFixture(t, "fairness")
	f.evidence["control_complete"] = true
	for _, m := range f.registry.Snapshot().Modules {
		for _, task := range m.Tasks {
			if task.Expected > 0 {
				require.LessOrEqual(t, task.Active, int64(task.Expected), "owner %s", task.Task)
			}
		}
	}
	require.NoError(t, f.projector.Stop(f.ctx))
	require.Eventually(t, func() bool { return f.registry.Snapshot().TotalActive == 0 }, time.Second, time.Millisecond)
}
