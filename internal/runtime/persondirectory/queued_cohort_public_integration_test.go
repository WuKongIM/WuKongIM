//go:build integration

package persondirectory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/persondirectory"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	channelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// cohortScanGate withholds one real leader-list result only while direct work
// is controlled, so recovery cannot admit the fixture's unsubmitted tasks.
type cohortScanGate struct {
	*cluster.Node
	once        sync.Once
	entered     chan struct{}
	release     chan struct{}
	expired     atomic.Bool
	readyReturn *membershipReturnControl
}

func (s *cohortScanGate) LocalLeaderHashSlots(ctx context.Context) ([]metadb.HashSlot, error) {
	result, err := s.Node.LocalLeaderHashSlots(ctx)
	s.once.Do(func() {
		close(s.entered)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-s.release:
		case <-ctx.Done():
		case <-timer.C:
			s.expired.Store(true)
		}
	})
	return result, err
}

// CompletePersonDirectoryTasks holds only the return from an already durable
// completion. No Slot credit, FSM lock or storage operation remains held.
func (s *cohortScanGate) CompletePersonDirectoryTasks(ctx context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	errs := s.Node.CompletePersonDirectoryTasks(ctx, tasks)
	control := s.readyReturn
	if control == nil || len(errs) != len(tasks) {
		return errs
	}
	for i, task := range tasks {
		if task.ChannelID != control.fast || errs[i] != nil {
			continue
		}
		close(control.entered)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-control.release:
		case <-ctx.Done():
			control.expired.Store(true)
		case <-timer.C:
			control.expired.Store(true)
		}
		break
	}
	return errs
}

// cohortPressure witnesses complete actual admission, never goroutine counts
// or a scheduling sleep. Existing public absolute pressure is the oracle.
type cohortPressure struct {
	want       int
	once       sync.Once
	ready      chan struct{}
	prefix     chan struct{}
	prefixOnce sync.Once
	bad        atomic.Bool
}

func (p *cohortPressure) ObservePersonDirectoryPressure(v persondirectory.PressureObservation) {
	if v.Capacity != 512 || v.Workers != 8 || v.Inflight > 512 {
		p.bad.Store(true)
	}
	if v.Inflight == p.want-1 {
		p.prefixOnce.Do(func() { close(p.prefix) })
	}
	if v.Inflight == p.want {
		p.once.Do(func() { close(p.ready) })
	}
}

// cohortMembershipGate holds eight actual successful writes independently.
// Every queued batch still uses the real cluster writer and durable source.
type cohortMembershipGate struct {
	writer       persondirectory.MembershipWriter
	mu           sync.Mutex
	next         int
	entered      chan int
	releases     [8]chan struct{}
	once         [8]sync.Once
	expired      atomic.Bool
	queued       []int
	independence *membershipReturnControl
}

// membershipReturnControl holds one already durable, unrelated result.
type membershipReturnControl struct {
	fast, slow       string
	entered, release chan struct{}
	once             sync.Once
	expired          atomic.Bool
	afterReady       bool
}

func (c *membershipReturnControl) unblock() { c.once.Do(func() { close(c.release) }) }

func (g *cohortMembershipGate) unblock(i int) { g.once[i].Do(func() { close(g.releases[i]) }) }
func (g *cohortMembershipGate) EnsureUserChannelMembershipBatch(ctx context.Context, items []metadb.UserChannelMembership) []persondirectory.MembershipResult {
	result := make([]persondirectory.MembershipResult, len(items))
	g.EnsureUserChannelMembershipWaves(ctx, items, func(_ context.Context, indices []int, err error) {
		for _, i := range indices {
			result[i].Err = err
		}
	})
	return result
}

// EnsureUserChannelMembershipWaves offers real aligned results and holds only
// the sibling's return when the two independent requests share one batch.
func (g *cohortMembershipGate) EnsureUserChannelMembershipWaves(ctx context.Context, items []metadb.UserChannelMembership, emit func(context.Context, []int, error)) {
	result := g.writer.EnsureUserChannelMembershipBatch(ctx, items)
	valid := len(result) == len(items)
	for _, r := range result {
		valid = valid && r.Err == nil
	}
	if !valid {
		for i, r := range result {
			emit(ctx, []int{i}, r.Err)
		}
		return
	}
	g.mu.Lock()
	id := g.next
	g.next++
	if id >= 8 {
		channels := map[string]struct{}{}
		for _, r := range items {
			channels[r.ChannelID] = struct{}{}
		}
		g.queued = append(g.queued, len(channels))
	}
	g.mu.Unlock()
	if id < 8 {
		g.entered <- id
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-g.releases[id]:
		case <-ctx.Done():
		case <-timer.C:
			g.expired.Store(true)
		}
	}
	control := g.independence
	var hasFast, hasSlow bool
	if control != nil {
		for _, item := range items {
			hasFast = hasFast || item.ChannelID == control.fast
			hasSlow = hasSlow || item.ChannelID == control.slow
		}
	}
	fast := make([]int, 0, len(items))
	slow := make([]int, 0, len(items))
	for i := range result {
		if hasFast && hasSlow && items[i].ChannelID == control.slow {
			slow = append(slow, i)
		} else {
			fast = append(fast, i)
		}
	}
	emit(ctx, fast, nil)
	if len(slow) == 0 {
		return
	}
	if !control.afterReady {
		close(control.entered)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-control.release:
		case <-ctx.Done():
			control.expired.Store(true)
		case <-timer.C:
			control.expired.Store(true)
		}
	}
	emit(ctx, slow, nil)
}

// Failure inventory: partial admission, control expiry, boundary/write/read
// failure, cancellation, incomplete joins or artifact failure invalidate this
// resource experiment. Only 73 actual accepted tasks, eight held successful
// writes, all Ready/UID proofs and cleanup can certify fragmented cohort RED.
func TestDirectoryProjectorCoalescesAlreadyQueuedRequests(t *testing.T) {
	runQueuedCohort(t, []int{16, 16, 16, 16}, []int{1, 64}, noMembershipControl)
}

// An unfitting FIFO head must remain owned and complete within the original
// 64-task page bound; it must not disappear or form an oversized projection.
func TestDirectoryProjectorQueuedRequestsRespectPageLimit(t *testing.T) {
	runQueuedCohort(t, []int{40, 40}, nil, noMembershipControl)
}

// A direct request must not wait after its memberships are durable just
// because a separate request was collected in the same projector batch.
func TestDirectoryProjectorReadyIndependentOfSiblingMembershipReturn(t *testing.T) {
	runQueuedCohort(t, []int{1, 1}, nil, separateRequests)
}

// Two tasks of one blocked Prepare must not create a partial Ready write.
// The unrelated caller must still complete from the same projector cohort.
func TestDirectoryProjectorKeepsReadyWritesGroupedByRequest(t *testing.T) {
	runQueuedCohort(t, []int{2, 1}, nil, sameRequest)
}

// A delayed Ready return for one request must not suppress the result of an
// independent request whose memberships have already committed in the same
// real worker cohort. Incomplete proofs, expired controls and cleanup failures
// invalidate the regression instead of reporting a timing failure.
func TestDirectoryProjectorReadyIndependentOfSiblingReadyReturn(t *testing.T) {
	runQueuedCohort(t, []int{1, 1}, nil, separateReadyReturns)
}

type membershipControlMode uint8

const (
	noMembershipControl membershipControlMode = iota
	separateRequests
	sameRequest
	separateReadyReturns
)

func runQueuedCohort(t *testing.T, batchSizes, expectedSizes []int, mode membershipControlMode) {
	t.Helper()
	count := 9 // eight held workers plus one trailing admission witness.
	for _, size := range batchSizes {
		count += size
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	var callers sync.WaitGroup
	report := map[string]any{"hash_slots": 256, "nodes": 1, "workers": 8, "capacity": 512, "valid": false, "resource_red": false}
	var n *cluster.Node
	var p *persondirectory.Projector
	scan := &cohortScanGate{entered: make(chan struct{}), release: make(chan struct{})}
	var scanOnce sync.Once
	gate := &cohortMembershipGate{entered: make(chan int, 8)}
	for i := range gate.releases {
		gate.releases[i] = make(chan struct{})
	}
	var proofComplete, controlComplete, independentBeforeRelease, readySplit bool
	var sizes []int
	dir := os.Getenv("WK_PERSON_DIRECTORY_TEST_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	t.Cleanup(func() {
		if gate.independence != nil {
			gate.independence.unblock()
		}
		for i := range gate.releases {
			gate.unblock(i)
		}
		scanOnce.Do(func() { close(scan.release) })
		cancel()
		callers.Wait()
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		joined := true
		if p != nil {
			joined = p.Stop(stop) == nil
		}
		if n != nil {
			joined = n.Stop(stop) == nil && joined
		}
		controlExpired := gate.expired.Load() || scan.expired.Load() || gate.independence != nil && gate.independence.expired.Load()
		valid := joined && proofComplete && controlComplete && !controlExpired
		report["cleanup_joined"] = joined
		report["control_expired"] = controlExpired
		if mode != noMembershipControl {
			report["independent_ready_before_release"] = independentBeforeRelease
			report["same_request_split_ready"] = readySplit
			report["behavior_red"] = valid && (!independentBeforeRelease || readySplit)
		}
		report["valid"] = valid
		report["resource_red"] = valid && expectedSizes != nil && !slices.Equal(sizes, expectedSizes)
		report["queued_cohort_sizes"] = sizes
		report["passed"] = valid && !t.Failed()
		require.NoError(t, os.MkdirAll(dir, 0700))
		raw, e := json.MarshalIndent(report, "", "  ")
		require.NoError(t, e)
		// Only a fully written and closed file may be published as final.
		// Link is exclusive: a previous report is never overwritten.
		final := filepath.Join(dir, t.Name()+".json")
		partial := final + ".partial"
		f, e := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		require.NoError(t, e)
		_, e = f.Write(append(raw, '\n'))
		ce := f.Close()
		require.NoError(t, e)
		require.NoError(t, ce)
		require.NoError(t, os.Link(partial, final))
		require.NoError(t, os.Remove(partial))
		require.True(t, joined, "invalid cleanup")
	})
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, e)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	n, e = cluster.New(cluster.Config{NodeID: 1, ListenAddr: addr, DataDir: t.TempDir(), Control: cluster.ControlConfig{ClusterID: "projector-cohort-public", AllowBootstrap: true, Voters: []cluster.ControlVoter{{NodeID: 1, Addr: addr}}}, Slots: cluster.SlotConfig{InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1}})
	require.NoError(t, e)
	scan.Node = n
	require.NoError(t, n.Start(ctx))
	require.Eventually(t, func() bool { return n.ProbeWriteReady(ctx) == nil }, 20*time.Second, 20*time.Millisecond)
	tasks := cohortTasks("queued-cohort", count)
	if mode != noMembershipControl {
		gate.independence = &membershipReturnControl{
			fast: tasks[8].ChannelID, slow: tasks[9].ChannelID,
			entered: make(chan struct{}), release: make(chan struct{}), afterReady: mode == separateReadyReturns,
		}
		if gate.independence.afterReady {
			scan.readyReturn = gate.independence
		}
	}
	for start := 0; start < len(tasks); start += 64 {
		rows := tasks[start:min(start+64, len(tasks))]
		errs := n.AdmitPersonDirectoryTasks(ctx, rows)
		require.Len(t, errs, len(rows))
		for _, err := range errs {
			require.NoError(t, err)
		}
	}
	byID := map[string]metadb.PersonDirectoryTaskEntry{}
	hashSlots := map[metadb.HashSlot]bool{}
	for _, task := range tasks {
		route, err := n.RouteKey(task.ChannelID)
		require.NoError(t, err)
		hashSlots[metadb.HashSlot(route.HashSlot)] = true
	}
	for slot := range hashSlots {
		rows, _, done, err := n.ListPersonDirectoryTaskPage(ctx, slot, metadb.PersonDirectoryTaskCursor{}, 64)
		require.NoError(t, err)
		require.True(t, done)
		for _, task := range rows {
			byID[task.ChannelID] = metadb.PersonDirectoryTaskEntry{HashSlot: slot, Task: task}
		}
	}
	require.Len(t, byID, count)
	items := make([]metadb.PersonDirectoryTaskEntry, count)
	for i, task := range tasks {
		items[i] = byID[task.ChannelID]
	}
	pressure := &cohortPressure{want: count, ready: make(chan struct{}), prefix: make(chan struct{})}
	gate.writer = clusterinfra.NewPersonDirectoryMembershipWriter(n)
	p, e = persondirectory.New(persondirectory.Options{Source: scan, Memberships: gate, Observer: pressure})
	require.NoError(t, e)
	require.NoError(t, p.Start(ctx))
	select {
	case <-scan.entered:
	case <-ctx.Done():
		t.Fatal("invalid scanner control")
	}
	prepare := func(rows []metadb.PersonDirectoryTaskEntry) <-chan []error {
		done := make(chan []error, 1)
		callers.Add(1)
		go func() { defer callers.Done(); done <- p.Prepare(ctx, rows) }()
		return done
	}
	held := make([]<-chan []error, 8)
	heldIDs := map[int]bool{}
	// Occupy each actual worker before submitting the next independent head.
	// A candidate may legitimately coalesce concurrently posted idle requests.
	for i := range held {
		held[i] = prepare(items[i : i+1])
		timer := time.NewTimer(2 * time.Second)
		select {
		case id := <-gate.entered:
			heldIDs[id] = true
		case <-held[i]:
			t.Fatal("invalid: initial real writer did not enter control")
		case <-timer.C:
			t.Fatal("invalid: worker control entry deadline")
		case <-ctx.Done():
			t.Fatal("invalid worker controls")
		}
		timer.Stop()
		require.False(t, gate.expired.Load() || scan.expired.Load(), "invalid expired controls")
	}
	require.Len(t, heldIDs, 8)
	queued := make([]<-chan []error, len(batchSizes)+1)
	start := 8
	for i, size := range batchSizes {
		queued[i] = prepare(items[start : start+size])
		start += size
	}
	// The final admission can only occur after all prefix batches have been
	// dispatched: the scheduler disables direct admission while one is pending.
	// Submit the trailing task only after all prefix tasks are accepted.
	select {
	case <-pressure.prefix:
	case <-ctx.Done():
		t.Fatal("invalid partial prefix admission")
	}
	queued[len(batchSizes)] = prepare(items[count-1 : count])
	select {
	case <-pressure.ready:
	case <-ctx.Done():
		t.Fatal("invalid partial admission")
	}
	require.False(t, pressure.bad.Load())
	report["actual_inflight_witness"] = count
	report["held_successful_writers"] = 8
	gate.unblock(0)
	if mode != noMembershipControl {
		control := gate.independence
		select {
		case <-control.entered:
		case <-time.After(time.Second):
			t.Fatal("invalid: independent requests did not share the projection")
		}
		fastCaller, heldCaller := 0, 1
		if mode == sameRequest {
			fastCaller, heldCaller = 1, 0
			metadata, err := n.GetChannelMetadataAuthoritative(ctx, tasks[8].ChannelID, 1)
			require.NoError(t, err)
			readySplit = metadata.DirectoryProjectionState == metadb.DirectoryProjectionReady
		}
		if mode == separateReadyReturns {
			fastCaller, heldCaller = 1, 0
			first, err := n.GetChannelMetadataAuthoritative(ctx, tasks[8].ChannelID, 1)
			require.NoError(t, err)
			require.Equal(t, metadb.DirectoryProjectionReady, first.DirectoryProjectionState,
				"invalid: selected Ready was not committed before holding its return")
			left, right, err := channelid.DecodePersonChannel(tasks[9].ChannelID)
			require.NoError(t, err)
			for _, uid := range []string{left, right} {
				row, found, err := n.GetUserChannelMembership(ctx, uid, tasks[9].ChannelID, 1)
				require.NoError(t, err)
				require.True(t, found, "invalid: independent membership was not durable before the hold")
				require.Equal(t, items[9].Task.Generation, row.SourceVersion)
			}
			report["held_ready_already_durable"] = true
			report["independent_memberships_already_durable"] = true
		}
		timer := time.NewTimer(400 * time.Millisecond)
		select {
		case errs := <-queued[fastCaller]:
			require.Len(t, errs, 1)
			require.NoError(t, errs[0])
			independentBeforeRelease = true
			queued[fastCaller] = nil
		case <-timer.C:
		case <-ctx.Done():
			t.Fatal("invalid: context expired")
		}
		timer.Stop()
		select {
		case <-queued[heldCaller]:
			t.Fatal("invalid: sibling returned before release")
		default:
		}
		control.unblock()
	}
	for _, done := range queued {
		if done == nil {
			continue
		}
		select {
		case errors := <-done:
			for _, err := range errors {
				require.NoError(t, err)
			}
		case <-ctx.Done():
			t.Fatal("queued callers did not finish")
		}
	}
	gate.mu.Lock()
	sizes = append([]int(nil), gate.queued...)
	gate.mu.Unlock()
	sort.Ints(sizes)
	controlComplete = !gate.expired.Load() && !scan.expired.Load()
	require.True(t, controlComplete, "invalid expired control")
	for i := range gate.releases {
		gate.unblock(i)
	}
	scanOnce.Do(func() { close(scan.release) })
	for _, done := range held {
		select {
		case errors := <-done:
			for _, err := range errors {
				require.NoError(t, err)
			}
		case <-ctx.Done():
			t.Fatal("held callers did not finish")
		}
	}
	reads := make([]slotproxy.PermissionMetadataRead, count)
	for i, task := range tasks {
		reads[i] = slotproxy.PermissionMetadataRead{Kind: slotproxy.PermissionMetadataReadChannel, ChannelID: task.ChannelID, ChannelType: 1}
	}
	facts := n.ReadSendPermissionMetadataBatch(ctx, reads)
	require.Len(t, facts, count)
	for i, fact := range facts {
		require.NoError(t, fact.Err)
		require.True(t, fact.Found)
		require.Equal(t, metadb.DirectoryProjectionReady, fact.Channel.DirectoryProjectionState)
		require.True(t, fact.PersonDirectoryFence.Valid())
		left, right, err := channelid.DecodePersonChannel(tasks[i].ChannelID)
		require.NoError(t, err)
		for _, uid := range []string{left, right} {
			row, found, err := n.GetUserChannelMembership(ctx, uid, tasks[i].ChannelID, 1)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, items[i].Task.Generation, row.SourceVersion)
		}
	}
	proofComplete = true
	report["fresh_ready_fences"] = count
	report["durable_uid_memberships"] = 2 * count
	total := 0
	for _, size := range sizes {
		require.Positive(t, size)
		require.LessOrEqual(t, size, 64, "original page bound")
		total += size
	}
	require.Equal(t, count-8, total, "all queued tasks retain ownership")
	if mode == separateReadyReturns {
		require.True(t, independentBeforeRelease, "independent durable Ready waited for the unrelated Ready return")
	} else if mode != noMembershipControl {
		require.True(t, independentBeforeRelease, "independent durable Ready waited for the unrelated membership return")
		require.False(t, readySplit, "partial Ready was committed while the same Prepare still waited for another task")
	}
	if expectedSizes != nil && !slices.Equal(sizes, expectedSizes) {
		t.Errorf("already-admitted tasks repeated or oversized durable waves: got %v, want %v", sizes, expectedSizes)
	}
}

func cohortTasks(prefix string, count int) []metadb.PersonDirectoryTask {
	tasks := make([]metadb.PersonDirectoryTask, count)
	for i := range tasks {
		tasks[i] = metadb.PersonDirectoryTask{ChannelID: channelid.EncodePersonChannel(fmt.Sprintf("%s-left-%d", prefix, i), fmt.Sprintf("%s-right-%d", prefix, i)), ChannelType: 1, CreatedAt: 1}
	}
	return tasks
}
