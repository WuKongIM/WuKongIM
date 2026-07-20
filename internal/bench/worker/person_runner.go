package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var errTargetUnavailable = errors.New("target unavailable")

const (
	defaultWorkerTrafficTimeout = 5 * time.Second
	clientAckTimeoutSlack       = time.Second
)

// WorkloadClientFactory builds benchmark clients for the default worker runner.
type WorkloadClientFactory func(user benchworkload.ConnectionUser, addr string) (benchworkload.ConnectionClient, error)

// PrepareChannelsRunner optionally prepares owner-only channel metadata before full prepare.
type PrepareChannelsRunner interface {
	// PrepareChannels upserts channel metadata required before subscribers are appended.
	PrepareChannels(ctx context.Context, assignment Assignment) error
}

// defaultWorkloadRunner builds and runs built-in workloads from worker assignments.
type defaultWorkloadRunner struct {
	clientFactory WorkloadClientFactory

	mu sync.Mutex
	// replaceMu serializes traffic generation swaps that reuse one connection manager.
	replaceMu sync.Mutex
	// metrics stores worker-level counters for connection lifecycle events.
	metrics *metrics.Registry
	// runID is the assignment currently bound to manager and workloads.
	runID string
	// manager owns the connected benchmark sessions for the active run.
	manager *benchworkload.ConnectionManager
	// reconnectedUsers counts repaired sessions across the active run.
	reconnectedUsers uint64
	// personWorkloads contains the active person traffic executors for the active run.
	personWorkloads []*benchworkload.PersonWorkload
	// groupWorkloads contains the active group traffic executors for the active run.
	groupWorkloads []*benchworkload.GroupWorkload
	// archivedWorkloadMetrics contains at most one temporally merged snapshot of
	// completed traffic generations, keeping long-running churn memory bounded.
	archivedWorkloadMetrics []metrics.SnapshotData
	// autoRecvAck controls and joins background recv-ack drains bound to the current run.
	autoRecvAck *benchworkload.AutoRecvAckHandle
	// teardownErr preserves a terminal resource-release failure for this run so
	// a retry cannot falsely acknowledge stopped after references were detached.
	teardownErr error
}

// personWorkloadBundle binds one profile shard, one traffic stream, and its pairs.
type personWorkloadBundle struct {
	profile model.ProfileShard
	traffic model.TrafficConfig
	pairs   []benchworkload.PersonPair
}

// personExecutionPlan contains the users to connect and workloads to build.
type personExecutionPlan struct {
	bundles []personWorkloadBundle
	users   []benchworkload.ConnectionUser
}

type churnReplacement struct {
	offset        int
	identityIndex int
	oldUID        string
	user          benchworkload.ConnectionUser
}

// NewDefaultWorkloadRunner builds the built-in workload runner for in-process callers.
func NewDefaultWorkloadRunner(factory WorkloadClientFactory) WorkloadRunner {
	return &defaultWorkloadRunner{clientFactory: factory, metrics: metrics.NewRegistry()}
}

func newDefaultWorkloadRunner(factory WorkloadClientFactory) WorkloadRunner {
	return NewDefaultWorkloadRunner(factory)
}

func (r *defaultWorkloadRunner) BeginAssignment(assignment Assignment) {
	r.beginRun(assignment.RunID, true)
}

// EndAssignment releases connections and background receive drains for a
// terminal assignment while retaining its bounded metrics for report reads.
func (r *defaultWorkloadRunner) EndAssignment(assignment Assignment) error {
	return r.closeCurrent(assignment.RunID)
}

func (r *defaultWorkloadRunner) Prepare(ctx context.Context, assignment Assignment) error {
	if err := prepareBenchTokens(ctx, assignment); err != nil {
		return err
	}
	return prepareGroupData(ctx, assignment)
}

func (r *defaultWorkloadRunner) PrepareChannels(ctx context.Context, assignment Assignment) error {
	return prepareGroupChannels(ctx, assignment)
}

func (r *defaultWorkloadRunner) Connect(ctx context.Context, assignment Assignment) error {
	r.beginRun(assignment.RunID, false)
	plan, err := buildPersonExecutionPlan(assignment)
	if err != nil {
		return err
	}
	groupPlan, err := buildGroupExecutionPlan(assignment)
	if err != nil {
		return err
	}
	users := mergeConnectionUsers(plan.users, groupPlan.users, identityRangeUsers(assignment))
	if len(users) == 0 {
		r.reset(assignment.RunID)
		return nil
	}
	manager, err := benchworkload.NewConnectionManager(connectionManagerConfig(assignment, r.clientFactory))
	if err != nil {
		return err
	}
	if err := manager.Connect(ctx, users); err != nil {
		_ = manager.Close()
		r.mergeConnectionMetrics(manager)
		return markTargetUnavailable(err)
	}
	if err := r.rebuildTrafficFromManager(assignment, manager); err != nil {
		_ = manager.Close()
		r.mergeConnectionMetrics(manager)
		return err
	}
	return nil
}

func connectionManagerConfig(assignment Assignment, factory WorkloadClientFactory) benchworkload.ConnectionManagerConfig {
	client := assignment.Client
	if client != nil {
		cloned := *client
		client = &cloned
	}
	tcpSource := assignment.TCPSource
	if tcpSource != nil {
		cloned := *tcpSource
		cloned.IPv4Addrs = append([]string(nil), tcpSource.IPv4Addrs...)
		tcpSource = &cloned
	}
	return benchworkload.ConnectionManagerConfig{
		Target:           assignment.Target,
		GatewayBalance:   assignment.Scenario.Online.GatewayBalance,
		ConnectRate:      assignment.Scenario.Online.ConnectRate,
		Heartbeat:        assignment.Scenario.Online.Heartbeat,
		Client:           client,
		TCPSource:        tcpSource,
		ClientFactory:    factory,
		Token:            "",
		OperationTimeout: 0,
		AckTimeout:       connectionAckTimeout(assignment),
	}
}

func connectionAckTimeout(assignment Assignment) time.Duration {
	var maxTimeout time.Duration
	for _, traffic := range assignment.Scenario.Messages.Traffic {
		timeout := traffic.AckTimeout
		if timeout <= 0 {
			timeout = defaultWorkerTrafficTimeout
		}
		if warmup := assignment.Scenario.Run.Warmup; warmup > timeout {
			timeout = warmup
		}
		if timeout > maxTimeout {
			maxTimeout = timeout
		}
	}
	if maxTimeout <= 0 {
		return 0
	}
	return maxTimeout + clientAckTimeoutSlack
}

// ConnectionStatus returns the current active connection count and reconnect churn.
func (r *defaultWorkloadRunner) ConnectionStatus() (int, uint64) {
	r.mu.Lock()
	manager := r.manager
	reconnected := atomic.LoadUint64(&r.reconnectedUsers)
	r.mu.Unlock()
	if manager == nil {
		return 0, reconnected
	}
	return manager.ActiveCount(), reconnected
}

// ResetTraffic rebuilds traffic workloads while keeping the active sessions open.
func (r *defaultWorkloadRunner) ResetTraffic(assignment Assignment) error {
	manager, err := r.managerForRun(assignment.RunID)
	if err != nil {
		return err
	}
	return r.rebuildTrafficFromManager(assignment, manager)
}

// RecoverTraffic repairs failed sessions and rebuilds workloads for the next traffic window.
func (r *defaultWorkloadRunner) RecoverTraffic(ctx context.Context, assignment Assignment, cause error) error {
	manager, err := r.managerForRun(assignment.RunID)
	if err != nil {
		return err
	}
	if err := r.repairSessions(ctx, assignment, manager, cause); err != nil {
		return markTargetUnavailable(err)
	}
	return r.rebuildTrafficFromManager(assignment, manager)
}

func (r *defaultWorkloadRunner) managerForRun(runID string) (*benchworkload.ConnectionManager, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runID != runID || r.manager == nil {
		return nil, fmt.Errorf("worker runner: no active sessions for run %q", runID)
	}
	return r.manager, nil
}

func (r *defaultWorkloadRunner) repairSessions(ctx context.Context, assignment Assignment, manager *benchworkload.ConnectionManager, cause error) error {
	repairUIDs := benchworkload.SessionErrorUIDs(cause)
	if len(repairUIDs) == 0 {
		return nil
	}
	users, err := connectionUsersForAssignment(assignment)
	if err != nil {
		return err
	}
	usersByUID := make(map[string]benchworkload.ConnectionUser, len(users))
	for _, user := range users {
		usersByUID[user.UID] = user
	}
	repairUsers := make([]benchworkload.ConnectionUser, 0, len(repairUIDs))
	for _, uid := range repairUIDs {
		user, ok := usersByUID[uid]
		if !ok {
			return fmt.Errorf("worker runner: failed session %q is not in assignment", uid)
		}
		repairUsers = append(repairUsers, user)
	}
	if err := manager.ReconnectUsers(ctx, repairUsers); err != nil {
		return err
	}
	atomic.AddUint64(&r.reconnectedUsers, uint64(len(repairUsers)))
	return nil
}

func (r *defaultWorkloadRunner) rebuildTrafficFromManager(assignment Assignment, manager *benchworkload.ConnectionManager) error {
	plan, err := buildPersonExecutionPlan(assignment)
	if err != nil {
		return err
	}
	groupPlan, err := buildGroupExecutionPlan(assignment)
	if err != nil {
		return err
	}
	users := mergeConnectionUsers(plan.users, groupPlan.users, identityRangeUsers(assignment))
	if len(users) == 0 {
		return r.replaceTrafficGeneration(assignment.RunID, manager, nil, nil, nil)
	}
	rawClients, err := personClientsFromManager(manager, users)
	if err != nil {
		return err
	}
	clients := benchworkload.WrapPersonClientsForConcurrentReads(rawClients)
	workloads, err := buildPersonWorkloads(assignment, plan.bundles, clients)
	if err != nil {
		return err
	}
	groupWorkloads, err := buildGroupWorkloads(assignment, groupPlan.bundles, clients)
	if err != nil {
		return err
	}
	var startAutoRecvAck func() *benchworkload.AutoRecvAckHandle
	if assignmentWantsRecvDrain(assignment) {
		startAutoRecvAck = func() *benchworkload.AutoRecvAckHandle {
			return benchworkload.StartAutoRecvAckHandleWithOptions(autoRecvAckClients(clients, plan.users, groupPlan.users), autoRecvAckOptionsForAssignment(assignment))
		}
	}
	return r.replaceTrafficGeneration(assignment.RunID, manager, workloads, groupWorkloads, startAutoRecvAck)
}

// MetricsSnapshot returns the merged metrics from active worker-local workloads.
func (r *defaultWorkloadRunner) MetricsSnapshot() metrics.SnapshotData {
	manager, personWorkloads, groupWorkloads, archived, registry := r.metricsState()
	workloadWindows := append([]metrics.SnapshotData(nil), archived...)
	if active, ok, err := spatialWorkloadMetrics(personWorkloads, groupWorkloads); err != nil {
		return emptyWorkerMetricsSnapshot()
	} else if ok {
		workloadWindows = append(workloadWindows, active)
	}
	workerSnapshots := make([]metrics.WorkerSnapshot, 0, 3)
	if registry != nil {
		workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: registry.Collect()})
	}
	if manager != nil {
		workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: manager.MetricsSnapshot()})
	}
	if len(workloadWindows) > 0 {
		workload, err := mergeTemporalWorkloadMetrics(workloadWindows)
		if err != nil {
			return emptyWorkerMetricsSnapshot()
		}
		workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: workload})
	}
	agg, err := metrics.Aggregate(workerSnapshots)
	if err != nil {
		return emptyWorkerMetricsSnapshot()
	}
	return agg
}

func emptyWorkerMetricsSnapshot() metrics.SnapshotData {
	return metrics.SnapshotData{
		Counters:   map[string]uint64{},
		Gauges:     map[string]float64{},
		Histograms: map[string]metrics.HistogramSummary{},
	}
}

func spatialWorkloadMetrics(personWorkloads []*benchworkload.PersonWorkload, groupWorkloads []*benchworkload.GroupWorkload) (metrics.SnapshotData, bool, error) {
	snapshots := make([]metrics.WorkerSnapshot, 0, len(personWorkloads)+len(groupWorkloads))
	for _, workload := range personWorkloads {
		if workload != nil && workload.Metrics() != nil {
			snapshots = append(snapshots, metrics.WorkerSnapshot{Metrics: workload.Metrics().Collect()})
		}
	}
	for _, workload := range groupWorkloads {
		if workload != nil && workload.Metrics() != nil {
			snapshots = append(snapshots, metrics.WorkerSnapshot{Metrics: workload.Metrics().Collect()})
		}
	}
	if len(snapshots) == 0 {
		return metrics.SnapshotData{}, false, nil
	}
	aggregated, err := metrics.Aggregate(snapshots)
	return aggregated, true, err
}

// mergeTemporalWorkloadMetrics combines sequential workload generations.
// Counters and histograms accumulate, while gauges retain the largest
// generation-local value instead of summing mutually exclusive windows.
func mergeTemporalWorkloadMetrics(windows []metrics.SnapshotData) (metrics.SnapshotData, error) {
	snapshots := make([]metrics.WorkerSnapshot, 0, len(windows))
	for _, window := range windows {
		snapshots = append(snapshots, metrics.WorkerSnapshot{Metrics: window})
	}
	aggregated, err := metrics.Aggregate(snapshots)
	if err != nil {
		return metrics.SnapshotData{}, err
	}
	aggregated.Gauges = make(map[string]float64)
	seen := make(map[string]struct{})
	for _, window := range windows {
		for key, value := range window.Gauges {
			if _, ok := seen[key]; !ok || value > aggregated.Gauges[key] {
				aggregated.Gauges[key] = value
				seen[key] = struct{}{}
			}
		}
	}
	return aggregated, nil
}

func (r *defaultWorkloadRunner) metricsState() (*benchworkload.ConnectionManager, []*benchworkload.PersonWorkload, []*benchworkload.GroupWorkload, []metrics.SnapshotData, *metrics.Registry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager,
		append([]*benchworkload.PersonWorkload(nil), r.personWorkloads...),
		append([]*benchworkload.GroupWorkload(nil), r.groupWorkloads...),
		append([]metrics.SnapshotData(nil), r.archivedWorkloadMetrics...),
		r.metrics
}

func (r *defaultWorkloadRunner) workerMetrics() metrics.SnapshotData {
	r.mu.Lock()
	registry := r.metrics
	r.mu.Unlock()
	if registry == nil {
		return metrics.SnapshotData{Counters: map[string]uint64{}, Gauges: map[string]float64{}, Histograms: map[string]metrics.HistogramSummary{}}
	}
	return registry.Collect()
}

func (r *defaultWorkloadRunner) currentRunID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runID
}

func (r *defaultWorkloadRunner) Warmup(ctx context.Context, assignment Assignment) error {
	return r.runPhaseWithIdleHold(ctx, assignment, assignment.Scenario.Run.Warmup, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Warmup(phaseCtx)
		}
		return group.Warmup(phaseCtx)
	})
}

func (r *defaultWorkloadRunner) Run(ctx context.Context, assignment Assignment) error {
	if assignment.Scenario.Online.Churn.Enabled {
		return r.runWithScheduledChurn(ctx, assignment)
	}
	return r.runPhaseWithIdleHold(ctx, assignment, assignment.Scenario.Run.Duration, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Run(phaseCtx)
		}
		return group.Run(phaseCtx)
	})
}

func (r *defaultWorkloadRunner) runWithScheduledChurn(ctx context.Context, assignment Assignment) error {
	churn := assignment.Scenario.Online.Churn
	remaining := assignment.Scenario.Run.Duration
	if remaining <= 0 || churn.Interval <= 0 {
		return fmt.Errorf("worker runner: scheduled churn requires positive duration and interval")
	}
	identityCount := assignment.Plan.IdentityRange.Len()
	if identityCount <= 0 {
		return sleepContext(ctx, remaining)
	}
	assignment.Plan.OnlineIdentityIndexes = make([]int, identityCount)
	for offset := range assignment.Plan.OnlineIdentityIndexes {
		assignment.Plan.OnlineIdentityIndexes[offset] = assignment.Plan.IdentityRange.Start + offset
	}
	swapGenerations := make([]int, identityCount)
	for cycle := 1; remaining > 0; cycle++ {
		window := min(remaining, churn.Interval)
		if err := r.runPhaseWithIdleHold(ctx, assignment, window, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
			if person != nil {
				return person.RunMeasuredWindow(phaseCtx, window, cycle)
			}
			return group.RunMeasuredWindow(phaseCtx, window, cycle)
		}); err != nil {
			return err
		}
		remaining -= window
		if remaining <= 0 {
			return nil
		}
		if err := r.applyScheduledChurn(ctx, &assignment, cycle, swapGenerations); err != nil {
			return markTargetUnavailable(err)
		}
	}
	return nil
}

func (r *defaultWorkloadRunner) applyScheduledChurn(ctx context.Context, assignment *Assignment, cycle int, swapGenerations []int) error {
	manager, err := r.managerForRun(assignment.RunID)
	if err != nil {
		return err
	}
	churn := assignment.Scenario.Online.Churn
	identityCount := assignment.Plan.IdentityRange.Len()
	churnCount := int(math.Round(float64(identityCount) * churn.Ratio))
	if churnCount < 1 {
		churnCount = 1
	}
	if churnCount > identityCount {
		churnCount = identityCount
	}
	sameUserCount := int(math.Round(float64(churnCount) * churn.SameUserRatio))
	if sameUserCount < 0 {
		sameUserCount = 0
	}
	if sameUserCount > churnCount {
		sameUserCount = churnCount
	}
	swapCount := churnCount - sameUserCount
	start := ((cycle - 1) * churnCount) % identityCount
	selectedOffsets := make([]int, churnCount)
	for index := range selectedOffsets {
		selectedOffsets[index] = (start + index) % identityCount
	}

	sameUsers := make([]benchworkload.ConnectionUser, 0, sameUserCount)
	for _, offset := range selectedOffsets[:sameUserCount] {
		identityIndex := assignment.Plan.OnlineIdentityIndexes[offset]
		sameUsers = append(sameUsers, connectionUserForIdentityIndex(assignment.Scenario.Identity, identityIndex))
	}
	if err := manager.ReconnectUsers(ctx, sameUsers); err != nil {
		return err
	}

	onlineTotal := assignment.Scenario.Online.TotalUsers
	totalUsers := assignment.Scenario.Identity.TotalUsers
	offlineLanes := 0
	if onlineTotal > 0 {
		offlineLanes = (totalUsers - onlineTotal) / onlineTotal
	}
	if swapCount > 0 && offlineLanes <= 0 {
		return fmt.Errorf("worker runner: identity churn requires at least one offline identity lane")
	}
	replacements := make([]churnReplacement, 0, swapCount)
	for _, offset := range selectedOffsets[sameUserCount:] {
		logicalIndex := assignment.Plan.IdentityRange.Start + offset
		generation := swapGenerations[offset] % offlineLanes
		newIdentityIndex := onlineTotal + generation*onlineTotal + logicalIndex
		oldIdentityIndex := assignment.Plan.OnlineIdentityIndexes[offset]
		replacements = append(replacements, churnReplacement{
			offset:        offset,
			identityIndex: newIdentityIndex,
			oldUID:        indexedID(assignment.Scenario.Identity.UIDPrefix, oldIdentityIndex),
			user:          connectionUserForIdentityIndex(assignment.Scenario.Identity, newIdentityIndex),
		})
		swapGenerations[offset]++
	}
	if err := prepareChurnTokens(ctx, *assignment, cycle, replacements); err != nil {
		return err
	}
	for _, item := range replacements {
		if _, err := manager.ReplaceUser(ctx, item.oldUID, item.user); err != nil {
			return err
		}
		assignment.Plan.OnlineIdentityIndexes[item.offset] = item.identityIndex
	}
	if err := prepareChurnGroupSubscriberSwaps(ctx, *assignment, cycle, replacements); err != nil {
		return err
	}

	if err := r.rebuildTrafficFromManager(*assignment, manager); err != nil {
		return err
	}
	r.mu.Lock()
	registry := r.metrics
	r.mu.Unlock()
	if registry != nil {
		registry.IncCounter("churn_window_total", nil)
		registry.AddCounter("churn_same_user_total", nil, uint64(sameUserCount))
		registry.AddCounter("churn_identity_swap_total", nil, uint64(swapCount))
	}
	atomic.AddUint64(&r.reconnectedUsers, uint64(churnCount))
	return nil
}

func (r *defaultWorkloadRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	err := r.runPhaseWithIdleHold(ctx, assignment, assignment.Scenario.Run.Cooldown, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Cooldown(phaseCtx)
		}
		return group.Cooldown(phaseCtx)
	})
	return errors.Join(err, r.closeCurrent(assignment.RunID))
}

func (r *defaultWorkloadRunner) runPhase(ctx context.Context, assignment Assignment, fn func(context.Context, *benchworkload.PersonWorkload, *benchworkload.GroupWorkload) error) error {
	return r.runPhaseWithIdleHold(ctx, assignment, 0, fn)
}

func (r *defaultWorkloadRunner) runPhaseWithIdleHold(ctx context.Context, assignment Assignment, idleDuration time.Duration, fn func(context.Context, *benchworkload.PersonWorkload, *benchworkload.GroupWorkload) error) error {
	personWorkloads, groupWorkloads, ok := r.snapshot(assignment.RunID)
	if !ok {
		if err := r.Connect(ctx, assignment); err != nil {
			return err
		}
		personWorkloads, groupWorkloads, ok = r.snapshot(assignment.RunID)
		if !ok {
			return nil
		}
	}
	if len(personWorkloads)+len(groupWorkloads) == 0 {
		return sleepContext(ctx, idleDuration)
	}
	phaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, len(personWorkloads)+len(groupWorkloads))
	recordError := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
			cancel()
		default:
		}
	}
	for _, wl := range personWorkloads {
		wl := wl
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordError(fn(phaseCtx, wl, nil))
		}()
	}
	for _, wl := range groupWorkloads {
		wl := wl
		wg.Add(1)
		go func() {
			defer wg.Done()
			recordError(fn(phaseCtx, nil, wl))
		}()
	}
	wg.Wait()
	close(errCh)
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func buildPersonExecutionPlan(assignment Assignment) (personExecutionPlan, error) {
	workerPlan := assignment.Plan
	if len(workerPlan.Profiles) == 0 {
		return personExecutionPlan{}, nil
	}
	trafficByProfile := make(map[string][]model.TrafficConfig, len(assignment.Scenario.Messages.Traffic))
	for _, traffic := range assignment.Scenario.Messages.Traffic {
		ref := strings.TrimSpace(traffic.ChannelRef)
		if ref == "" {
			continue
		}
		trafficByProfile[ref] = append(trafficByProfile[ref], traffic)
	}
	profileNames := make([]string, 0, len(workerPlan.Profiles))
	for profileName := range workerPlan.Profiles {
		profileNames = append(profileNames, profileName)
	}
	sort.Strings(profileNames)

	plan := personExecutionPlan{}
	seenUsers := make(map[string]struct{})
	addUser := func(user benchworkload.ConnectionUser) {
		if _, ok := seenUsers[user.UID]; ok {
			return
		}
		seenUsers[user.UID] = struct{}{}
		plan.users = append(plan.users, user)
	}

	for _, profileName := range profileNames {
		profile := workerPlan.Profiles[profileName]
		if profile.ChannelType != model.ChannelTypePerson {
			continue
		}
		pairs, users, err := personPairsAndUsersForProfile(profile, assignment.Scenario.Identity, assignment.Plan)
		if err != nil {
			return personExecutionPlan{}, fmt.Errorf("person profile %q: %w", profileName, err)
		}
		if len(pairs) == 0 {
			continue
		}
		for _, user := range users {
			addUser(user)
		}
		trafficItems := trafficByProfile[profileName]
		if len(trafficItems) == 0 {
			return personExecutionPlan{}, fmt.Errorf("person profile %q has assigned channels but no matching traffic", profileName)
		}
		for _, traffic := range trafficItems {
			plan.bundles = append(plan.bundles, personWorkloadBundle{profile: profile, traffic: traffic, pairs: pairs})
		}
	}
	return plan, nil
}

func buildPersonWorkloads(assignment Assignment, bundles []personWorkloadBundle, clients map[string]benchworkload.PersonClient) ([]*benchworkload.PersonWorkload, error) {
	workloads := make([]*benchworkload.PersonWorkload, 0, len(bundles))
	for _, bundle := range bundles {
		wl, err := benchworkload.NewPersonWorkload(benchworkload.PersonConfig{
			RunID:            assignment.RunID,
			ProfileName:      bundle.profile.Name,
			TrafficName:      bundle.traffic.Name,
			ClientMsgPrefix:  assignment.Scenario.Identity.ClientMsgPrefix,
			DevicePrefix:     assignment.Scenario.Identity.DevicePrefix,
			PayloadSizeBytes: assignment.Scenario.Messages.Payload.SizeBytes,
			Rate:             bundle.traffic.RatePerChannel,
			MaxConcurrency:   bundle.traffic.Concurrency,
			RunDuration:      assignment.Scenario.Run.Duration,
			WarmupDuration:   assignment.Scenario.Run.Warmup,
			CooldownDuration: assignment.Scenario.Run.Cooldown,
			AckTimeout:       bundle.traffic.AckTimeout,
			RecvTimeout:      bundle.traffic.RecvTimeout,
			VerifyRecvMode:   bundle.traffic.Verify.Recv.Mode,
			RecvAck:          bundle.traffic.RecvAck,
			Pairs:            bundle.pairs,
			Metrics:          metrics.NewRegistry(),
		}, clients)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, wl)
	}
	return workloads, nil
}

func personPairsAndUsersForProfile(profile model.ProfileShard, identity model.IdentityConfig, workerPlan model.WorkerPlan) ([]benchworkload.PersonPair, []benchworkload.ConnectionUser, error) {
	channelCount := profile.ChannelRange.Len()
	if channelCount <= 0 {
		return nil, nil, nil
	}
	if profile.ParticipantRange.Len() < channelCount*2 {
		return nil, nil, fmt.Errorf("participant range %v is too small for %d person channels", profile.ParticipantRange, channelCount)
	}
	pairs := make([]benchworkload.PersonPair, 0, channelCount)
	users := make([]benchworkload.ConnectionUser, 0, channelCount*2)
	seen := make(map[string]struct{}, channelCount*2)
	for idx := 0; idx < channelCount; idx++ {
		channelIndex := profile.ChannelRange.Start + idx
		logicalSenderIndex := profile.ParticipantRange.Start + idx*2
		logicalRecipientIndex := logicalSenderIndex + 1
		senderIndex := mappedOnlineIdentityIndex(workerPlan, logicalSenderIndex)
		recipientIndex := mappedOnlineIdentityIndex(workerPlan, logicalRecipientIndex)
		senderUID := indexedID(identity.UIDPrefix, senderIndex)
		recipientUID := indexedID(identity.UIDPrefix, recipientIndex)
		pairs = append(pairs, benchworkload.PersonPair{
			ChannelIndex: channelIndex,
			SenderUID:    senderUID,
			RecipientUID: recipientUID,
		})
		for _, item := range []struct {
			uid   string
			index int
		}{
			{uid: senderUID, index: senderIndex},
			{uid: recipientUID, index: recipientIndex},
		} {
			if _, ok := seen[item.uid]; ok {
				continue
			}
			seen[item.uid] = struct{}{}
			users = append(users, benchworkload.ConnectionUser{
				UID:      item.uid,
				DeviceID: indexedID(identity.DevicePrefix, item.index),
				Token:    personToken(identity.Token.Mode, item.uid),
			})
		}
	}
	return pairs, users, nil
}

func personClientsFromManager(manager *benchworkload.ConnectionManager, users []benchworkload.ConnectionUser) (map[string]benchworkload.PersonClient, error) {
	clients := make(map[string]benchworkload.PersonClient, len(users))
	for _, user := range users {
		session, ok := manager.Session(user.UID)
		if !ok || session == nil || session.Client == nil {
			return nil, fmt.Errorf("person workload: missing session for %q", user.UID)
		}
		client, ok := session.Client.(benchworkload.PersonClient)
		if !ok {
			return nil, fmt.Errorf("person workload: client for %q does not support person traffic", user.UID)
		}
		clients[user.UID] = client
	}
	return clients, nil
}

func indexedID(prefix string, index int) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "bench"
	}
	return fmt.Sprintf("%s-%d", prefix, index)
}

func personToken(mode, uid string) string {
	switch strings.TrimSpace(mode) {
	case "", "none":
		return ""
	case "bench_api":
		return fmt.Sprintf("bench-token-%s", uid)
	default:
		return ""
	}
}

func prepareBenchTokens(ctx context.Context, assignment Assignment) error {
	if strings.TrimSpace(assignment.Scenario.Identity.Token.Mode) != "bench_api" {
		return nil
	}
	users, err := connectionUsersForAssignment(assignment)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}
	client := groupPrepareClient(assignment.Target)
	const batchSize = 1000
	for start := 0; start < len(users); start += batchSize {
		end := start + batchSize
		if end > len(users) {
			end = len(users)
		}
		req := model.BatchTokensRequest{
			RunID:   assignment.RunID,
			BatchID: fmt.Sprintf("%s-tokens-%s-%d-%d", assignment.RunID, assignment.WorkerID, start, end),
			Upsert:  true,
			Users:   make([]model.UserTokenItem, 0, end-start),
		}
		for _, user := range users[start:end] {
			req.Users = append(req.Users, model.UserTokenItem{UID: user.UID, Token: user.Token})
		}
		if err := client.UpsertTokens(ctx, req); err != nil {
			return fmt.Errorf("prepare bench tokens: %w", err)
		}
	}
	return nil
}

func connectionUsersForAssignment(assignment Assignment) ([]benchworkload.ConnectionUser, error) {
	plan, err := buildPersonExecutionPlan(assignment)
	if err != nil {
		return nil, err
	}
	groupPlan, err := buildGroupExecutionPlan(assignment)
	if err != nil {
		return nil, err
	}
	return mergeConnectionUsers(plan.users, groupPlan.users, identityRangeUsers(assignment)), nil
}

func identityRangeUsers(assignment Assignment) []benchworkload.ConnectionUser {
	identityRange := assignment.Plan.IdentityRange
	if identityRange.Len() <= 0 {
		return nil
	}
	identity := assignment.Scenario.Identity
	users := make([]benchworkload.ConnectionUser, 0, identityRange.Len())
	for idx := identityRange.Start; idx < identityRange.End; idx++ {
		identityIndex := mappedOnlineIdentityIndex(assignment.Plan, idx)
		uid := indexedID(identity.UIDPrefix, identityIndex)
		users = append(users, benchworkload.ConnectionUser{
			UID:      uid,
			DeviceID: indexedID(identity.DevicePrefix, identityIndex),
			Token:    personToken(identity.Token.Mode, uid),
		})
	}
	return users
}

func connectionUserForIdentityIndex(identity model.IdentityConfig, identityIndex int) benchworkload.ConnectionUser {
	uid := indexedID(identity.UIDPrefix, identityIndex)
	return benchworkload.ConnectionUser{
		UID:      uid,
		DeviceID: indexedID(identity.DevicePrefix, identityIndex),
		Token:    personToken(identity.Token.Mode, uid),
	}
}

func prepareChurnTokens(ctx context.Context, assignment Assignment, cycle int, replacements []churnReplacement) error {
	if strings.TrimSpace(assignment.Scenario.Identity.Token.Mode) != "bench_api" || len(replacements) == 0 {
		return nil
	}
	request := model.BatchTokensRequest{
		RunID:   assignment.RunID,
		BatchID: fmt.Sprintf("%s-churn-tokens-%s-%d", assignment.RunID, assignment.WorkerID, cycle),
		Upsert:  true,
		Users:   make([]model.UserTokenItem, 0, len(replacements)),
	}
	for _, replacement := range replacements {
		request.Users = append(request.Users, model.UserTokenItem{UID: replacement.user.UID, Token: replacement.user.Token})
	}
	if err := groupPrepareClient(assignment.Target).UpsertTokens(ctx, request); err != nil {
		return fmt.Errorf("prepare churn tokens: %w", err)
	}
	return nil
}

// prepareChurnGroupSubscriberSwaps keeps durable group membership aligned with identity-swap connections.
func prepareChurnGroupSubscriberSwaps(ctx context.Context, assignment Assignment, cycle int, replacements []churnReplacement) error {
	if len(replacements) == 0 {
		return nil
	}
	replacementByUID := make(map[string]churnReplacement, len(replacements))
	for _, replacement := range replacements {
		replacementByUID[replacement.user.UID] = replacement
	}
	profiles := scenarioProfilesByName(assignment.Scenario)
	itemsByChannel := make(map[string]model.SubscriberItem)
	for _, profileName := range sortedProfileNames(assignment.Plan.Profiles) {
		shard := assignment.Plan.Profiles[profileName]
		if shard.ChannelType != model.ChannelTypeGroup {
			continue
		}
		profile, ok := profiles[profileName]
		if !ok {
			return fmt.Errorf("prepare churn group subscribers: profile %q missing from scenario", profileName)
		}
		channels := groupChannelsForShard(assignment.RunID, shard, profile, assignment.Scenario.Identity, assignment.Scenario.Online.TotalUsers, assignment.Plan)
		for _, channel := range channels {
			item := model.SubscriberItem{ChannelID: channel.ChannelID, ChannelType: frame.ChannelTypeGroup}
			for _, uid := range channel.OnlineMembers {
				if _, ok := replacementByUID[uid]; ok {
					item.Subscribers = append(item.Subscribers, uid)
				}
			}
			if len(item.Subscribers) > 0 {
				itemsByChannel[channel.ChannelID] = item
			}
		}
	}
	if len(itemsByChannel) == 0 {
		return nil
	}
	channelIDs := make([]string, 0, len(itemsByChannel))
	for channelID := range itemsByChannel {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Strings(channelIDs)
	addItems := make([]model.SubscriberItem, 0, len(channelIDs))
	removeItems := make([]model.SubscriberItem, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		addItem := itemsByChannel[channelID]
		removeItem := model.SubscriberItem{ChannelID: addItem.ChannelID, ChannelType: addItem.ChannelType, Subscribers: make([]string, 0, len(addItem.Subscribers))}
		for _, uid := range addItem.Subscribers {
			removeItem.Subscribers = append(removeItem.Subscribers, replacementByUID[uid].oldUID)
		}
		addItems = append(addItems, addItem)
		removeItems = append(removeItems, removeItem)
	}
	client := groupPrepareClient(assignment.Target)
	if err := mutateChurnGroupSubscribers(ctx, client.AddSubscribers, assignment, cycle, "add", addItems); err != nil {
		return fmt.Errorf("prepare churn group subscribers: add replacements: %w", err)
	}
	if err := mutateChurnGroupSubscribers(ctx, client.RemoveSubscribers, assignment, cycle, "remove", removeItems); err != nil {
		return fmt.Errorf("prepare churn group subscribers: remove replaced users: %w", err)
	}
	return nil
}

func mutateChurnGroupSubscribers(ctx context.Context, mutate func(context.Context, model.BatchSubscribersRequest) error, assignment Assignment, cycle int, operation string, items []model.SubscriberItem) error {
	const batchSize = 1000
	for start := 0; start < len(items); start += batchSize {
		end := min(start+batchSize, len(items))
		if err := mutate(ctx, model.BatchSubscribersRequest{
			RunID:   assignment.RunID,
			BatchID: fmt.Sprintf("%s-churn-subs-%s-%s-%d-%d-%d", assignment.RunID, operation, assignment.WorkerID, cycle, start, end),
			Items:   append([]model.SubscriberItem(nil), items[start:end]...),
		}); err != nil {
			return err
		}
	}
	return nil
}

func mappedOnlineIdentityIndex(workerPlan model.WorkerPlan, logicalIndex int) int {
	if len(workerPlan.OnlineIdentityIndexes) != workerPlan.IdentityRange.Len() || logicalIndex < workerPlan.IdentityRange.Start || logicalIndex >= workerPlan.IdentityRange.End {
		return logicalIndex
	}
	return workerPlan.OnlineIdentityIndexes[logicalIndex-workerPlan.IdentityRange.Start]
}

func (r *defaultWorkloadRunner) replaceTrafficGeneration(runID string, manager *benchworkload.ConnectionManager, personWorkloads []*benchworkload.PersonWorkload, groupWorkloads []*benchworkload.GroupWorkload, startAutoRecvAck func() *benchworkload.AutoRecvAckHandle) error {
	r.replaceMu.Lock()
	defer r.replaceMu.Unlock()

	r.mu.Lock()
	if r.runID != runID {
		r.mu.Unlock()
		return fmt.Errorf("worker runner: cannot replace traffic for inactive run %q", runID)
	}
	previousAutoRecvAck := r.autoRecvAck
	previousManager := r.manager
	r.autoRecvAck = nil
	r.mu.Unlock()

	if previousAutoRecvAck != nil {
		previousAutoRecvAck.Cancel()
	}
	if previousManager != nil && previousManager != manager {
		_ = previousManager.Close()
	}
	if previousAutoRecvAck != nil {
		previousAutoRecvAck.Wait()
	}

	var autoRecvAck *benchworkload.AutoRecvAckHandle
	if startAutoRecvAck != nil {
		autoRecvAck = startAutoRecvAck()
	}
	r.mu.Lock()
	if r.runID != runID || (previousManager != nil && r.manager != previousManager) {
		r.mu.Unlock()
		if autoRecvAck != nil {
			autoRecvAck.Cancel()
			autoRecvAck.Wait()
		}
		return fmt.Errorf("worker runner: traffic generation changed while replacing run %q", runID)
	}
	if err := r.archiveCurrentWorkloadMetricsLocked(); err != nil {
		r.mu.Unlock()
		if autoRecvAck != nil {
			autoRecvAck.Cancel()
			autoRecvAck.Wait()
		}
		return err
	}
	r.manager = manager
	r.personWorkloads = personWorkloads
	r.groupWorkloads = groupWorkloads
	r.autoRecvAck = autoRecvAck
	r.mu.Unlock()
	return nil
}

func (r *defaultWorkloadRunner) archiveCurrentWorkloadMetricsLocked() error {
	current, ok, err := spatialWorkloadMetrics(r.personWorkloads, r.groupWorkloads)
	if err != nil {
		return fmt.Errorf("worker runner: aggregate active workload metrics: %w", err)
	}
	if !ok {
		return nil
	}
	windows := append(append([]metrics.SnapshotData(nil), r.archivedWorkloadMetrics...), current)
	archived, err := mergeTemporalWorkloadMetrics(windows)
	if err != nil {
		return fmt.Errorf("worker runner: merge archived workload metrics: %w", err)
	}
	r.archivedWorkloadMetrics = []metrics.SnapshotData{archived}
	return nil
}

func (r *defaultWorkloadRunner) mergeConnectionMetrics(manager *benchworkload.ConnectionManager) {
	r.mu.Lock()
	registry := r.metrics
	r.mu.Unlock()
	if registry == nil || manager == nil {
		return
	}
	snap := manager.MetricsSnapshot()
	for key, value := range snap.Counters {
		registry.AddCounter(key, nil, value)
	}
	for _, sample := range snap.Errors {
		registry.RecordErrorSample(sample.Name, errors.New(sample.Message))
	}
}

func (r *defaultWorkloadRunner) snapshot(runID string) ([]*benchworkload.PersonWorkload, []*benchworkload.GroupWorkload, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runID != runID || (r.manager == nil && len(r.personWorkloads) == 0 && len(r.groupWorkloads) == 0) {
		return nil, nil, false
	}
	return append([]*benchworkload.PersonWorkload(nil), r.personWorkloads...), append([]*benchworkload.GroupWorkload(nil), r.groupWorkloads...), true
}

func (r *defaultWorkloadRunner) closeCurrent(runID string) error {
	r.mu.Lock()
	if r.runID != runID {
		r.mu.Unlock()
		return nil
	}
	if r.teardownErr != nil {
		err := r.teardownErr
		r.mu.Unlock()
		return err
	}
	autoRecvAck := r.autoRecvAck
	manager := r.manager
	archiveErr := r.archiveCurrentWorkloadMetricsLocked()
	r.autoRecvAck = nil
	r.manager = nil
	r.personWorkloads = nil
	r.groupWorkloads = nil
	r.mu.Unlock()

	if autoRecvAck != nil {
		autoRecvAck.Cancel()
	}
	var closeErr error
	if manager != nil {
		closeErr = manager.Close()
	}
	if autoRecvAck != nil {
		autoRecvAck.Wait()
	}
	if manager != nil {
		r.mergeConnectionMetrics(manager)
	}
	teardownErr := errors.Join(archiveErr, closeErr)
	if teardownErr != nil {
		r.mu.Lock()
		if r.runID == runID && r.teardownErr == nil {
			r.teardownErr = teardownErr
		}
		r.mu.Unlock()
	}
	return teardownErr
}

func markTargetUnavailable(err error) error {
	if err == nil {
		return nil
	}
	if benchworkload.IsTCPSourceError(err) {
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Errorf("%w: %v", errTargetUnavailable, err)
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "network is unreachable") {
		return fmt.Errorf("%w: %v", errTargetUnavailable, err)
	}
	return err
}

func (r *defaultWorkloadRunner) reset(runID string) {
	r.beginRun(runID, true)
}

func (r *defaultWorkloadRunner) beginRun(runID string, force bool) {
	r.mu.Lock()
	if !force && r.runID == runID {
		r.mu.Unlock()
		return
	}
	autoRecvAck := r.autoRecvAck
	manager := r.manager
	r.runID = runID
	r.manager = nil
	r.autoRecvAck = nil
	r.teardownErr = nil
	atomic.StoreUint64(&r.reconnectedUsers, 0)
	r.personWorkloads = nil
	r.groupWorkloads = nil
	r.archivedWorkloadMetrics = nil
	r.metrics = metrics.NewRegistry()
	r.mu.Unlock()

	if autoRecvAck != nil {
		autoRecvAck.Cancel()
	}
	if manager != nil {
		_ = manager.Close()
	}
	if autoRecvAck != nil {
		autoRecvAck.Wait()
	}
}

func assignmentWantsRecvAck(assignment Assignment) bool {
	for _, traffic := range assignment.Scenario.Messages.Traffic {
		if traffic.RecvAck {
			return true
		}
	}
	return false
}

func assignmentWantsRecvDrain(assignment Assignment) bool {
	for _, traffic := range assignment.Scenario.Messages.Traffic {
		if strings.TrimSpace(traffic.ChannelRef) != "" {
			return true
		}
	}
	return false
}

func autoRecvAckOptionsForAssignment(assignment Assignment) benchworkload.AutoRecvAckOptions {
	bufferChannelTypes := assignmentRecvVerificationChannelTypes(assignment)
	return benchworkload.AutoRecvAckOptions{
		BufferRecvFrames:       len(bufferChannelTypes) > 0,
		BufferRecvChannelTypes: bufferChannelTypes,
		DisableRecvAck:         !assignmentWantsRecvAck(assignment),
	}
}

func autoRecvAckClients(clients map[string]benchworkload.PersonClient, userGroups ...[]benchworkload.ConnectionUser) map[string]benchworkload.PersonClient {
	if len(clients) == 0 {
		return nil
	}
	selected := make(map[string]benchworkload.PersonClient)
	for _, users := range userGroups {
		for _, user := range users {
			uid := strings.TrimSpace(user.UID)
			if uid == "" {
				continue
			}
			client, ok := clients[uid]
			if !ok || client == nil {
				continue
			}
			selected[uid] = client
		}
	}
	return selected
}

func assignmentWantsRecvVerification(assignment Assignment) bool {
	return len(assignmentRecvVerificationChannelTypes(assignment)) > 0
}

func assignmentRecvVerificationChannelTypes(assignment Assignment) map[uint8]struct{} {
	profileTypes := make(map[string]string, len(assignment.Plan.Profiles)+len(assignment.Scenario.Channels.Profiles))
	for name, profile := range assignment.Plan.Profiles {
		profileTypes[name] = profile.ChannelType
	}
	for _, profile := range assignment.Scenario.Channels.Profiles {
		if _, exists := profileTypes[profile.Name]; !exists {
			profileTypes[profile.Name] = profile.ChannelType
		}
	}
	result := make(map[uint8]struct{}, 2)
	for _, traffic := range assignment.Scenario.Messages.Traffic {
		switch strings.ToLower(strings.TrimSpace(traffic.Verify.Recv.Mode)) {
		case "full", "sampled":
			switch strings.ToLower(strings.TrimSpace(profileTypes[traffic.ChannelRef])) {
			case model.ChannelTypePerson:
				result[uint8(frame.ChannelTypePerson)] = struct{}{}
			case model.ChannelTypeGroup:
				result[uint8(frame.ChannelTypeGroup)] = struct{}{}
			}
		}
	}
	return result
}

func mergeConnectionUsers(groups ...[]benchworkload.ConnectionUser) []benchworkload.ConnectionUser {
	seen := make(map[string]struct{})
	users := make([]benchworkload.ConnectionUser, 0)
	for _, group := range groups {
		for _, user := range group {
			if _, ok := seen[user.UID]; ok {
				continue
			}
			seen[user.UID] = struct{}{}
			users = append(users, user)
		}
	}
	return users
}
