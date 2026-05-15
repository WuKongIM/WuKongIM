package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
)

var errTargetUnavailable = errors.New("target unavailable")

// WorkloadClientFactory builds benchmark clients for the default worker runner.
type WorkloadClientFactory func(user benchworkload.ConnectionUser, addr string) (benchworkload.ConnectionClient, error)

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
	// personWorkloads contains the active person traffic executors for the active run.
	personWorkloads []*benchworkload.PersonWorkload
	// groupWorkloads contains the active group traffic executors for the active run.
	groupWorkloads []*benchworkload.GroupWorkload
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

func newDefaultWorkloadRunner(factory WorkloadClientFactory) WorkloadRunner {
	return &defaultWorkloadRunner{clientFactory: factory, metrics: metrics.NewRegistry()}
}

func (r *defaultWorkloadRunner) Prepare(ctx context.Context, assignment Assignment) error {
	return prepareGroupData(ctx, assignment)
}

func (r *defaultWorkloadRunner) Connect(ctx context.Context, assignment Assignment) error {
	plan, err := buildPersonExecutionPlan(assignment)
	if err != nil {
		return err
	}
	groupPlan, err := buildGroupExecutionPlan(assignment)
	if err != nil {
		return err
	}
	users := mergeConnectionUsers(plan.users, groupPlan.users)
	if len(users) == 0 {
		r.reset(assignment.RunID)
		return nil
	}
	manager, err := benchworkload.NewConnectionManager(benchworkload.ConnectionManagerConfig{
		Target:           assignment.Target,
		GatewayBalance:   assignment.Scenario.Online.GatewayBalance,
		ConnectRate:      assignment.Scenario.Online.ConnectRate,
		ClientFactory:    r.clientFactory,
		Token:            "",
		OperationTimeout: 0,
	})
	if err != nil {
		return err
	}
	if err := manager.Connect(ctx, users); err != nil {
		r.mergeConnectionMetrics(manager)
		_ = manager.Close()
		return markTargetUnavailable(err)
	}
	r.mergeConnectionMetrics(manager)
	clients, err := personClientsFromManager(manager, users)
	if err != nil {
		_ = manager.Close()
		return err
	}
	workloads, err := buildPersonWorkloads(assignment, plan.bundles, clients)
	if err != nil {
		_ = manager.Close()
		return err
	}
	groupWorkloads, err := buildGroupWorkloads(assignment, groupPlan.bundles, clients)
	if err != nil {
		_ = manager.Close()
		return err
	}
	r.store(assignment.RunID, manager, workloads, groupWorkloads)
	return nil
}

// MetricsSnapshot returns the merged metrics from active worker-local workloads.
func (r *defaultWorkloadRunner) MetricsSnapshot() metrics.SnapshotData {
	personWorkloads, groupWorkloads, _ := r.snapshot(r.currentRunID())
	workerSnapshots := make([]metrics.WorkerSnapshot, 0, len(personWorkloads)+len(groupWorkloads)+1)
	workerSnapshots = append(workerSnapshots, metrics.WorkerSnapshot{Metrics: r.workerMetrics()})
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
	return r.runPhase(ctx, assignment, func(person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Warmup(ctx)
		}
		return group.Warmup(ctx)
	})
}

func (r *defaultWorkloadRunner) Run(ctx context.Context, assignment Assignment) error {
	return r.runPhase(ctx, assignment, func(person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Run(ctx)
		}
		return group.Run(ctx)
	})
}

func (r *defaultWorkloadRunner) Cooldown(ctx context.Context, assignment Assignment) error {
	err := r.runPhase(ctx, assignment, func(person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
		if person != nil {
			return person.Cooldown(ctx)
		}
		return group.Cooldown(ctx)
	})
	r.closeCurrent(assignment.RunID)
	return err
}

func (r *defaultWorkloadRunner) runPhase(ctx context.Context, assignment Assignment, fn func(*benchworkload.PersonWorkload, *benchworkload.GroupWorkload) error) error {
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
	for _, wl := range personWorkloads {
		if err := fn(wl, nil); err != nil {
			return err
		}
	}
	for _, wl := range groupWorkloads {
		if err := fn(nil, wl); err != nil {
			return err
		}
	}
	return nil
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

func (r *defaultWorkloadRunner) store(runID string, manager *benchworkload.ConnectionManager, personWorkloads []*benchworkload.PersonWorkload, groupWorkloads []*benchworkload.GroupWorkload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.manager != nil && r.manager != manager {
		_ = r.manager.Close()
	}
	r.runID = runID
	r.manager = manager
	r.personWorkloads = personWorkloads
	r.groupWorkloads = groupWorkloads
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
	defer r.mu.Unlock()
	if r.runID != runID {
		return
	}
	if r.manager != nil {
		_ = r.manager.Close()
	}
	r.manager = nil
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.manager != nil {
		_ = r.manager.Close()
	}
	r.runID = runID
	r.manager = nil
	r.personWorkloads = nil
	r.groupWorkloads = nil
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
