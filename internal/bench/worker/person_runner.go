package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
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
	// cancelAutoRecvAck stops background recv-ack drains bound to the current run.
	cancelAutoRecvAck context.CancelFunc
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
	manager, err := benchworkload.NewConnectionManager(benchworkload.ConnectionManagerConfig{
		Target:           assignment.Target,
		GatewayBalance:   assignment.Scenario.Online.GatewayBalance,
		ConnectRate:      assignment.Scenario.Online.ConnectRate,
		Heartbeat:        assignment.Scenario.Online.Heartbeat,
		ClientFactory:    r.clientFactory,
		Token:            "",
		OperationTimeout: 0,
		AckTimeout:       connectionAckTimeout(assignment),
	})
	if err != nil {
		return err
	}
	if err := manager.Connect(ctx, users); err != nil {
		r.mergeConnectionMetrics(manager)
		_ = manager.Close()
		return markTargetUnavailable(err)
	}
	if err := r.rebuildTrafficFromManager(assignment, manager); err != nil {
		r.mergeConnectionMetrics(manager)
		_ = manager.Close()
		return err
	}
	return nil
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
		r.store(assignment.RunID, manager, nil, nil, nil)
		return nil
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
	var cancelAutoRecvAck context.CancelFunc
	if assignmentWantsRecvDrain(assignment) {
		cancelAutoRecvAck = benchworkload.StartAutoRecvAckWithOptions(autoRecvAckClients(clients, plan.users, groupPlan.users), autoRecvAckOptionsForAssignment(assignment))
	}
	r.store(assignment.RunID, manager, workloads, groupWorkloads, cancelAutoRecvAck)
	return nil
}

// MetricsSnapshot returns the merged metrics from active worker-local workloads.
func (r *defaultWorkloadRunner) MetricsSnapshot() metrics.SnapshotData {
	manager, personWorkloads, groupWorkloads, registry := r.metricsState()
	workerSnapshots := make([]metrics.WorkerSnapshot, 0, len(personWorkloads)+len(groupWorkloads)+2)
	if registry != nil {
		workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: registry.Collect()})
	}
	if manager != nil {
		workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: manager.MetricsSnapshot()})
	}
	for _, wl := range personWorkloads {
		workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: wl.Metrics().Collect()})
	}
	for _, wl := range groupWorkloads {
		workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: wl.Metrics().Collect()})
	}
	agg, err := metrics.Aggregate(workerSnapshots)
	if err != nil {
		return metrics.SnapshotData{Counters: map[string]uint64{}, Gauges: map[string]float64{}, Histograms: map[string]metrics.HistogramSummary{}}
	}
	return agg
}

func (r *defaultWorkloadRunner) metricsState() (*benchworkload.ConnectionManager, []*benchworkload.PersonWorkload, []*benchworkload.GroupWorkload, *metrics.Registry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manager,
		append([]*benchworkload.PersonWorkload(nil), r.personWorkloads...),
		append([]*benchworkload.GroupWorkload(nil), r.groupWorkloads...),
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
	return r.runPhaseWithIdleHold(ctx, assignment, assignment.Scenario.Run.Duration, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Run(phaseCtx)
		}
		return group.Run(phaseCtx)
	})
}

func (r *defaultWorkloadRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	err := r.runPhaseWithIdleHold(ctx, assignment, assignment.Scenario.Run.Cooldown, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Cooldown(phaseCtx)
		}
		return group.Cooldown(phaseCtx)
	})
	r.closeCurrent(assignment.RunID)
	return err
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
		pairs, users, err := personPairsAndUsersForProfile(profile, assignment.Scenario.Identity)
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

func personPairsAndUsersForProfile(profile model.ProfileShard, identity model.IdentityConfig) ([]benchworkload.PersonPair, []benchworkload.ConnectionUser, error) {
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
		participantIndex := profile.ParticipantRange.Start + idx*2
		senderUID := indexedID(identity.UIDPrefix, participantIndex)
		recipientUID := indexedID(identity.UIDPrefix, participantIndex+1)
		pairs = append(pairs, benchworkload.PersonPair{
			ChannelIndex: channelIndex,
			SenderUID:    senderUID,
			RecipientUID: recipientUID,
		})
		for _, item := range []struct {
			uid   string
			index int
		}{
			{uid: senderUID, index: participantIndex},
			{uid: recipientUID, index: participantIndex + 1},
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
		uid := indexedID(identity.UIDPrefix, idx)
		users = append(users, benchworkload.ConnectionUser{
			UID:      uid,
			DeviceID: indexedID(identity.DevicePrefix, idx),
			Token:    personToken(identity.Token.Mode, uid),
		})
	}
	return users
}

func (r *defaultWorkloadRunner) store(runID string, manager *benchworkload.ConnectionManager, personWorkloads []*benchworkload.PersonWorkload, groupWorkloads []*benchworkload.GroupWorkload, cancelAutoRecvAck context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancelAutoRecvAck != nil {
		r.cancelAutoRecvAck()
		r.cancelAutoRecvAck = nil
	}
	if r.manager != nil && r.manager != manager {
		_ = r.manager.Close()
	}
	r.runID = runID
	r.manager = manager
	r.personWorkloads = personWorkloads
	r.groupWorkloads = groupWorkloads
	r.cancelAutoRecvAck = cancelAutoRecvAck
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

func (r *defaultWorkloadRunner) closeCurrent(runID string) {
	r.mu.Lock()
	if r.runID != runID {
		r.mu.Unlock()
		return
	}
	cancelAutoRecvAck := r.cancelAutoRecvAck
	manager := r.manager
	r.cancelAutoRecvAck = nil
	r.manager = nil
	r.mu.Unlock()

	if cancelAutoRecvAck != nil {
		cancelAutoRecvAck()
	}
	if manager != nil {
		_ = manager.Close()
		r.mergeConnectionMetrics(manager)
	}
}

func markTargetUnavailable(err error) error {
	if err == nil {
		return nil
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
	defer r.mu.Unlock()
	if !force && r.runID == runID {
		return
	}
	if r.cancelAutoRecvAck != nil {
		r.cancelAutoRecvAck()
		r.cancelAutoRecvAck = nil
	}
	if r.manager != nil {
		_ = r.manager.Close()
	}
	r.runID = runID
	r.manager = nil
	atomic.StoreUint64(&r.reconnectedUsers, 0)
	r.personWorkloads = nil
	r.groupWorkloads = nil
	r.metrics = metrics.NewRegistry()
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
	return benchworkload.AutoRecvAckOptions{
		BufferRecvFrames: assignmentWantsRecvVerification(assignment),
		DisableRecvAck:   !assignmentWantsRecvAck(assignment),
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
	for _, traffic := range assignment.Scenario.Messages.Traffic {
		switch strings.ToLower(strings.TrimSpace(traffic.Verify.Recv.Mode)) {
		case "full", "sampled":
			return true
		}
	}
	return false
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
