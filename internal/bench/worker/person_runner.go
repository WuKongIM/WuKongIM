package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
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
	// terminalFencePrepare obtains one target-published grant only after the
	// product generation has closed and drained its write path. Tests replace
	// this black-box HTTP seam without importing product internals.
	terminalFencePrepare func(context.Context, Assignment, int) (frame.TerminalFenceGrant, error)

	mu sync.Mutex
	// replaceMu serializes traffic generation swaps that reuse one connection manager.
	replaceMu sync.Mutex
	// maintenanceMu serializes session repair/churn with generation replacement.
	maintenanceMu sync.Mutex
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
	// fanoutProof is one assignment-generation witness shared by every socket
	// wrapper and group workload rebuild. Churn must never reset it.
	fanoutProof *benchworkload.GroupFanoutProof
	// fanoutProofAssignmentID identifies the immutable assignment generation
	// whose traffic is allowed to contribute to fanoutProof.
	fanoutProofAssignmentID string
	// archivedWorkloadMetrics contains at most one temporally merged snapshot of
	// completed traffic generations, keeping long-running churn memory bounded.
	archivedWorkloadMetrics []metrics.SnapshotData
	// autoRecvAck controls and joins background recv-ack drains bound to the current run.
	autoRecvAck *benchworkload.AutoRecvAckHandle
	// receiveDrainRequired records whether the current assignment owns inbound
	// processing that must converge before a terminal pre-close cut.
	receiveDrainRequired bool
	// receiveDrainTerminal retains the latest bounded cooldown result, including
	// a non-converged failure snapshot.
	receiveDrainTerminal    model.ReceiveDrainSnapshot
	receiveDrainTerminalSet bool
	// receiveDrain historical counters retain failures and receive progress from
	// completed churn generations so a later clean generation cannot hide them
	// or make the typed lifecycle timeline regress.
	receiveDrainReadFailures   uint64
	receiveDrainACKFailures    uint64
	receiveDrainACKSuccesses   uint64
	receiveDrainFramesObserved uint64
	receiveDrainFramesDrained  uint64
	// receiveDrainEvidenceLost retains an incomplete queue contract from any
	// completed receive-drain generation.
	receiveDrainEvidenceLost bool
	// teardownErr preserves a terminal resource-release failure for this run so
	// a retry cannot falsely acknowledge stopped after references were detached.
	teardownErr error
	// measuredTask owns admitted measured work after the SEND admission window
	// closes. Cooldown joins this task before terminal evidence is collected;
	// EndAssignment is the only successful-path boundary that closes sessions.
	measuredTask *measuredTrafficTask
	// terminalCut pauses only explicitly opted-in local cooldowns after internal
	// convergence while an external observer captures exact pre-close evidence.
	terminalCut terminalCutBarrier
	// terminalCutRequiredConnections freezes the exact assignment-owned session
	// count that must still be live when an external terminal cut is accepted.
	terminalCutRequiredConnections int
	// terminalSealedLifecycle retains the pre-close side of a receive proof that
	// was verified unchanged after a server-confirmed ingress fence and reader join.
	terminalSealedLifecycle *LifecycleStatus
}

// measuredTrafficTask separates the fixed SEND-admission window from the
// bounded completion tail for SENDACK/RECV/RECVACK and retry processing.
type measuredTrafficTask struct {
	runID  string
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func (t *measuredTrafficTask) wait() error {
	if t == nil {
		return nil
	}
	<-t.done
	return t.err
}

func (t *measuredTrafficTask) completed() (bool, error) {
	if t == nil {
		return true, nil
	}
	select {
	case <-t.done:
		return true, t.err
	default:
		return false, nil
	}
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
	return &defaultWorkloadRunner{
		clientFactory:        factory,
		metrics:              metrics.NewRegistry(),
		terminalFencePrepare: prepareTargetTerminalFence,
	}
}

func prepareTargetTerminalFence(ctx context.Context, assignment Assignment, expectedSessions int) (frame.TerminalFenceGrant, error) {
	addrs := append([]string(nil), assignment.Target.BenchAPI.Addrs...)
	if len(addrs) == 0 {
		addrs = append(addrs, assignment.Target.API.Addrs...)
	}
	client := target.NewClient(target.Config{
		APIAddrs: addrs,
		Token:    assignment.Target.BenchAPI.Token,
		// The worker's bounded cooldown context is the only terminal deadline.
		// A shorter client-wide default must not truncate the reviewed 90s drain.
		HTTPClient: &http.Client{},
	})
	grant, err := client.PrepareTerminalFence(ctx, model.TerminalFencePrepareRequest{
		RunID: assignment.RunID, AssignmentID: assignment.AssignmentID, ExpectedSessions: expectedSessions,
	})
	if err != nil {
		return frame.TerminalFenceGrant{}, fmt.Errorf("worker runner: prepare target terminal fence: %w", err)
	}
	return frame.TerminalFenceGrant{Epoch: grant.Epoch, Capability: grant.Capability}, nil
}

func newDefaultWorkloadRunner(factory WorkloadClientFactory) WorkloadRunner {
	return NewDefaultWorkloadRunner(factory)
}

func (r *defaultWorkloadRunner) BeginAssignment(assignment Assignment) {
	r.beginRun(assignment.RunID, true)
	requiredConnections := assignment.Plan.IdentityRange.Len()
	if requiredConnections <= 0 && assignment.Scenario.Run.ExternalTerminalCut {
		requiredConnections = assignment.Scenario.Online.TotalUsers
	}
	r.mu.Lock()
	r.terminalCutRequiredConnections = requiredConnections
	r.mu.Unlock()
	r.terminalCut.begin(assignment)
}

// EndAssignment releases connections and background receive drains for a
// terminal assignment while retaining its bounded metrics for report reads.
func (r *defaultWorkloadRunner) EndAssignment(assignment Assignment) error {
	return r.closeCurrent(assignment.RunID)
}

func (r *defaultWorkloadRunner) Prepare(ctx context.Context, assignment Assignment) error {
	proof, err := fanoutProofForAssignment(assignment)
	if err != nil {
		return err
	}
	if err := r.installAssignmentFanoutProof(assignment, proof); err != nil {
		return err
	}
	if err := prepareBenchTokens(ctx, assignment); err != nil {
		return err
	}
	return prepareGroupData(ctx, assignment)
}

func (r *defaultWorkloadRunner) PrepareChannels(ctx context.Context, assignment Assignment) error {
	return prepareGroupChannels(ctx, assignment)
}

func fanoutProofForAssignment(assignment Assignment) (*benchworkload.GroupFanoutProof, error) {
	if !assignment.Scenario.Run.ExternalTerminalCut {
		return nil, nil
	}
	if !assignmentWantsRecvDrain(assignment) {
		return nil, fmt.Errorf("worker runner: external terminal cut requires receive-drain traffic")
	}
	if !assignmentWantsRecvAck(assignment) {
		return nil, fmt.Errorf("worker runner: external terminal cut requires recv_ack traffic")
	}
	if len(assignment.Scenario.Channels.Profiles) != 1 || len(assignment.Scenario.Messages.Traffic) != 1 {
		return nil, fmt.Errorf("worker runner: external terminal cut fanout proof requires exactly one group profile and one traffic stream")
	}
	profile := assignment.Scenario.Channels.Profiles[0]
	traffic := assignment.Scenario.Messages.Traffic[0]
	if profile.ChannelType != model.ChannelTypeGroup || strings.TrimSpace(profile.Name) == "" ||
		profile.Count <= 0 || strings.TrimSpace(traffic.ChannelRef) != strings.TrimSpace(profile.Name) || profile.Members.Count < 2 ||
		profile.Shard.Mode != "hash" || profile.Online.MemberRatio != 1 ||
		strings.TrimSpace(traffic.Verify.Recv.Mode) != "none" || !traffic.RecvAck {
		return nil, fmt.Errorf("worker runner: external terminal cut fanout proof requires one exact fully-online hash group stream")
	}
	groupPlan, err := buildGroupExecutionPlan(assignment)
	if err != nil {
		return nil, fmt.Errorf("worker runner: external terminal cut fanout plan: %w", err)
	}
	shard, shardOK := assignment.Plan.Profiles[profile.Name]
	if len(assignment.Plan.Profiles) != 1 || !shardOK || shard.ChannelType != model.ChannelTypeGroup ||
		shard.ChannelRange.Start != 0 || shard.ChannelRange.End != profile.Count ||
		shard.MemberRange != assignment.Plan.IdentityRange || shard.MemberReusePolicy != "allowed" ||
		shard.TrafficPartitionCount != 0 || len(shard.OwnedTrafficPartitions) != 0 ||
		len(groupPlan.bundles) != 1 || len(groupPlan.bundles[0].channels) != profile.Count {
		return nil, fmt.Errorf("worker runner: external terminal cut fanout proof requires one complete group shard")
	}
	for _, channel := range groupPlan.bundles[0].channels {
		if len(channel.OnlineMembers) != profile.Members.Count {
			return nil, fmt.Errorf("worker runner: external terminal cut fanout proof requires complete online group membership")
		}
	}
	proof, err := benchworkload.NewGroupFanoutProof(profile.Members.Count)
	if err != nil {
		return nil, fmt.Errorf("worker runner: create fanout proof: %w", err)
	}
	return proof, nil
}

func (r *defaultWorkloadRunner) installAssignmentFanoutProof(assignment Assignment, proof *benchworkload.GroupFanoutProof) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runID != assignment.RunID {
		return fmt.Errorf("worker runner: fanout proof assignment changed")
	}
	if assignment.Scenario.Run.ExternalTerminalCut {
		if proof == nil || strings.TrimSpace(assignment.AssignmentID) == "" {
			return fmt.Errorf("worker runner: external terminal cut fanout proof is unavailable")
		}
		r.fanoutProof = proof
		r.fanoutProofAssignmentID = assignment.AssignmentID
		return nil
	}
	r.fanoutProof = nil
	r.fanoutProofAssignmentID = assignment.AssignmentID
	return nil
}

func (r *defaultWorkloadRunner) Connect(ctx context.Context, assignment Assignment) error {
	r.beginRun(assignment.RunID, false)
	if assignment.Scenario.Run.ExternalTerminalCut && r.currentFanoutProof(assignment) == nil {
		proof, err := fanoutProofForAssignment(assignment)
		if err != nil {
			return err
		}
		if err := r.installAssignmentFanoutProof(assignment, proof); err != nil {
			return err
		}
	}
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
	if err := r.rebuildTrafficFromManager(ctx, assignment, manager); err != nil {
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

// LifecycleStatus returns the bounded measured-phase evidence consumed by the
// local baseline sampler. The projection is computed in Go from validated
// low-cardinality series; no identity-bearing metric or shell parsing is used.
func (r *defaultWorkloadRunner) LifecycleStatus() LifecycleStatus {
	r.mu.Lock()
	if r.terminalSealedLifecycle != nil {
		sealed := cloneLifecycleStatus(*r.terminalSealedLifecycle)
		r.mu.Unlock()
		return sealed
	}
	r.mu.Unlock()
	active, reconnected := r.ConnectionStatus()
	terminalCut := r.terminalCut.status()
	receiveDrain := r.receiveDrainSnapshot()
	return LifecycleStatus{
		ActiveConnections:     active,
		ReconnectedUsers:      reconnected,
		Traffic:               trafficStatusFromMetrics(r.MetricsSnapshot()),
		ReceiveDrain:          receiveDrain,
		ReceiveDrainSHA256:    model.ReceiveDrainFingerprint(receiveDrain),
		TerminalCutRequired:   terminalCut.Required,
		TerminalCutReady:      terminalCut.Ready,
		TerminalCutReadyAt:    terminalCut.ReadyAt,
		TerminalCutDeadlineAt: terminalCut.DeadlineAt,
		TerminalCut:           terminalCut.Binding,
	}
}

func cloneLifecycleStatus(status LifecycleStatus) LifecycleStatus {
	if status.TerminalCut != nil {
		binding := *status.TerminalCut
		status.TerminalCut = &binding
	}
	return status
}

// TerminalCutStatus returns the bounded state of the opt-in pre-close barrier.
func (r *defaultWorkloadRunner) TerminalCutStatus() TerminalCutStatus {
	return r.terminalCut.status()
}

// AcknowledgeTerminalCut immutably binds one exact external pre-close capture.
func (r *defaultWorkloadRunner) AcknowledgeTerminalCut(request TerminalCutRequest) (TerminalCutBinding, error) {
	r.mu.Lock()
	activeRun := r.runID
	requiredConnections := r.terminalCutRequiredConnections
	r.mu.Unlock()
	if strings.TrimSpace(request.RunID) == "" || request.RunID != activeRun {
		return TerminalCutBinding{}, ErrTerminalCutNotReady
	}
	// Preserve exact-request idempotency after the first accepted ACK. Live
	// sessions are intentionally allowed to close only after that immutable
	// binding releases cooldown.
	if r.terminalCut.status().Binding != nil {
		return r.terminalCut.acknowledge(request)
	}
	lifecycle := r.LifecycleStatus()
	if request.ReceiveDrainSHA256 != lifecycle.ReceiveDrainSHA256 {
		return TerminalCutBinding{}, ErrTerminalCutNotReady
	}
	if lifecycle.ReceiveDrain.Required && !lifecycle.ReceiveDrain.TerminalProofComplete() {
		r.mu.Lock()
		r.receiveDrainTerminal = lifecycle.ReceiveDrain
		r.receiveDrainTerminalSet = true
		r.mu.Unlock()
		return TerminalCutBinding{}, ErrTerminalCutNotReady
	}
	if !lifecycle.ReceiveDrain.FanoutProof.Required || !lifecycle.ReceiveDrain.FanoutProof.Complete() {
		return TerminalCutBinding{}, ErrTerminalCutNotReady
	}
	if lifecycle.Traffic.Remaining != 0 || !lifecycle.ReceiveDrain.Required ||
		requiredConnections <= 0 || lifecycle.ActiveConnections != requiredConnections ||
		lifecycle.ReceiveDrain.ClientCount != uint64(requiredConnections) ||
		lifecycle.ReceiveDrain.ActiveDrains != uint64(requiredConnections) ||
		lifecycle.ReceiveDrain.QueueSnapshotClients != uint64(requiredConnections) {
		return TerminalCutBinding{}, ErrTerminalCutNotReady
	}
	return r.terminalCut.acknowledge(request)
}

// SealTerminalReceive requests a server-confirmed ingress fence, stops and
// joins the exact assignment's receive readers, and retains the live proof only
// when the stopped cut reconciles with it. Unsupported transports fail closed
// without publishing terminal pre-close evidence.
func (r *defaultWorkloadRunner) SealTerminalReceive(ctx context.Context, assignment Assignment) error {
	r.replaceMu.Lock()
	defer r.replaceMu.Unlock()

	cut := r.terminalCut.status()
	if !cut.Required || cut.Binding == nil || cut.Binding.RunID != assignment.RunID ||
		cut.Binding.AssignmentID != assignment.AssignmentID {
		return ErrTerminalCutNotReady
	}
	r.mu.Lock()
	if r.runID != assignment.RunID {
		r.mu.Unlock()
		return ErrTerminalCutNotReady
	}
	handle := r.autoRecvAck
	manager := r.manager
	proof := r.fanoutProof
	requiredConnections := r.terminalCutRequiredConnections
	r.mu.Unlock()
	if handle == nil {
		snapshot := r.receiveDrainSnapshot()
		if snapshot.TerminalProofComplete() && snapshot.FanoutProof.Required && snapshot.FanoutProof.Complete() &&
			model.ReceiveDrainFingerprint(snapshot) == cut.Binding.ReceiveDrainSHA256 {
			return nil
		}
		return fmt.Errorf("worker runner: terminal receive evidence unavailable")
	}

	if manager == nil || manager.ActiveCount() != requiredConnections {
		return fmt.Errorf("worker runner: terminal receive ingress coverage changed")
	}
	// Cooldown already established the target-published fence on every live
	// session before exposing terminal-cut readiness. At stop we only rebuild
	// the exact receive proof, join its readers, and compare the immutable cut;
	// sending a second fence would create a different epoch boundary.
	drained, stopped, err := handle.DrainAndStop(ctx)
	drained = r.mergeReceiveDrainHistory(drained)
	drained = attachFanoutProof(drained, proof)
	stopped = attachFanoutProof(stopped, proof)
	if err == nil && model.ReceiveDrainFingerprint(drained) != cut.Binding.ReceiveDrainSHA256 {
		err = fmt.Errorf("terminal receive proof changed after acknowledgement")
	}
	r.archiveReceiveDrainGeneration(drained, stopped, err != nil)
	if err != nil {
		drained.EvidenceComplete = false
		drained.DrainComplete = false
		drained.StableZeroObservations = 0
	}
	traffic := trafficStatusFromMetrics(r.MetricsSnapshot())
	r.mu.Lock()
	if r.runID != assignment.RunID || r.autoRecvAck != handle {
		r.mu.Unlock()
		return fmt.Errorf("worker runner: receive generation changed while sealing run %q", assignment.RunID)
	}
	r.autoRecvAck = nil
	r.receiveDrainTerminal = drained
	r.receiveDrainTerminalSet = true
	if err == nil && drained.TerminalProofComplete() {
		binding := *cut.Binding
		r.terminalSealedLifecycle = &LifecycleStatus{
			ActiveConnections:     requiredConnections,
			ReconnectedUsers:      atomic.LoadUint64(&r.reconnectedUsers),
			Traffic:               traffic,
			ReceiveDrain:          drained,
			ReceiveDrainSHA256:    model.ReceiveDrainFingerprint(drained),
			TerminalCutRequired:   true,
			TerminalCutReady:      cut.Ready,
			TerminalCutReadyAt:    cut.ReadyAt,
			TerminalCutDeadlineAt: cut.DeadlineAt,
			TerminalCut:           &binding,
		}
	}
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("worker runner: seal terminal receive: %w", err)
	}
	if !drained.TerminalProofComplete() {
		return fmt.Errorf("worker runner: terminal receive proof is incomplete")
	}
	return nil
}

func (r *defaultWorkloadRunner) receiveDrainSnapshot() model.ReceiveDrainSnapshot {
	r.mu.Lock()
	required := r.receiveDrainRequired
	proof := r.fanoutProof
	handle := r.autoRecvAck
	terminal := r.receiveDrainTerminal
	terminalSet := r.receiveDrainTerminalSet
	readFailures := r.receiveDrainReadFailures
	ackFailures := r.receiveDrainACKFailures
	ackSuccesses := r.receiveDrainACKSuccesses
	framesObserved := r.receiveDrainFramesObserved
	framesDrained := r.receiveDrainFramesDrained
	evidenceLost := r.receiveDrainEvidenceLost
	r.mu.Unlock()

	if !required {
		return model.ReceiveDrainNotRequired()
	}
	if terminalSet && handle == nil {
		return attachFanoutProof(terminal, proof)
	}
	var snapshot model.ReceiveDrainSnapshot
	if handle != nil {
		snapshot = handle.Snapshot()
	} else {
		snapshot.Required = true
	}
	snapshot = mergeHistoricalReceiveDrain(snapshot, readFailures, ackFailures, ackSuccesses, framesObserved, framesDrained, evidenceLost)
	snapshot = attachFanoutProof(snapshot, proof)
	if terminalSet && snapshot.TerminalProofComplete() {
		r.mu.Lock()
		r.receiveDrainTerminal = snapshot
		r.mu.Unlock()
	}
	return snapshot
}

func attachFanoutProof(snapshot model.ReceiveDrainSnapshot, proof *benchworkload.GroupFanoutProof) model.ReceiveDrainSnapshot {
	if proof == nil {
		snapshot.FanoutProof = model.FanoutProofNotRequired()
		return snapshot
	}
	snapshot.FanoutProof = proof.Snapshot()
	return snapshot
}

func mergeHistoricalReceiveDrain(snapshot model.ReceiveDrainSnapshot, readFailures, ackFailures, ackSuccesses, framesObserved, framesDrained uint64, evidenceLost bool) model.ReceiveDrainSnapshot {
	if next, ok := checkedAddUint64(snapshot.ReadFailures, readFailures); ok {
		snapshot.ReadFailures = next
	} else {
		snapshot.ReadFailures = ^uint64(0)
		snapshot.EvidenceComplete = false
	}
	if next, ok := checkedAddUint64(snapshot.RecvACKFailures, ackFailures); ok {
		snapshot.RecvACKFailures = next
	} else {
		snapshot.RecvACKFailures = ^uint64(0)
		snapshot.EvidenceComplete = false
	}
	if next, ok := checkedAddUint64(snapshot.RecvACKSuccesses, ackSuccesses); ok {
		snapshot.RecvACKSuccesses = next
	} else {
		snapshot.RecvACKSuccesses = ^uint64(0)
		snapshot.EvidenceComplete = false
	}
	if next, ok := checkedAddUint64(snapshot.ReceiveFramesObserved, framesObserved); ok {
		snapshot.ReceiveFramesObserved = next
	} else {
		snapshot.ReceiveFramesObserved = ^uint64(0)
		snapshot.EvidenceComplete = false
	}
	if next, ok := checkedAddUint64(snapshot.BufferedFramesDrained, framesDrained); ok {
		snapshot.BufferedFramesDrained = next
	} else {
		snapshot.BufferedFramesDrained = ^uint64(0)
		snapshot.EvidenceComplete = false
	}
	if evidenceLost {
		snapshot.EvidenceComplete = false
	}
	if !snapshot.ZeroCutComplete() {
		snapshot.DrainComplete = false
		snapshot.StableZeroObservations = 0
	}
	return snapshot
}

func (r *defaultWorkloadRunner) mergeReceiveDrainHistory(snapshot model.ReceiveDrainSnapshot) model.ReceiveDrainSnapshot {
	r.mu.Lock()
	readFailures := r.receiveDrainReadFailures
	ackFailures := r.receiveDrainACKFailures
	ackSuccesses := r.receiveDrainACKSuccesses
	framesObserved := r.receiveDrainFramesObserved
	framesDrained := r.receiveDrainFramesDrained
	evidenceLost := r.receiveDrainEvidenceLost
	r.mu.Unlock()
	return mergeHistoricalReceiveDrain(snapshot, readFailures, ackFailures, ackSuccesses, framesObserved, framesDrained, evidenceLost)
}

func (r *defaultWorkloadRunner) archiveReceiveDrainGeneration(before, after model.ReceiveDrainSnapshot, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if failed || !before.EvidenceComplete || !after.EvidenceComplete {
		r.receiveDrainEvidenceLost = true
	}
	if next, ok := checkedAddUint64(r.receiveDrainReadFailures, after.ReadFailures); ok {
		r.receiveDrainReadFailures = next
	} else {
		r.receiveDrainReadFailures = ^uint64(0)
		r.receiveDrainEvidenceLost = true
	}
	if next, ok := checkedAddUint64(r.receiveDrainACKFailures, after.RecvACKFailures); ok {
		r.receiveDrainACKFailures = next
	} else {
		r.receiveDrainACKFailures = ^uint64(0)
		r.receiveDrainEvidenceLost = true
	}
	if next, ok := checkedAddUint64(r.receiveDrainACKSuccesses, after.RecvACKSuccesses); ok {
		r.receiveDrainACKSuccesses = next
	} else {
		r.receiveDrainACKSuccesses = ^uint64(0)
		r.receiveDrainEvidenceLost = true
	}
	if next, ok := checkedAddUint64(r.receiveDrainFramesObserved, after.ReceiveFramesObserved); ok {
		r.receiveDrainFramesObserved = next
	} else {
		r.receiveDrainFramesObserved = ^uint64(0)
		r.receiveDrainEvidenceLost = true
	}
	if next, ok := checkedAddUint64(r.receiveDrainFramesDrained, after.BufferedFramesDrained); ok {
		r.receiveDrainFramesDrained = next
	} else {
		r.receiveDrainFramesDrained = ^uint64(0)
		r.receiveDrainEvidenceLost = true
	}
}

func (r *defaultWorkloadRunner) storeReceiveDrainTerminal(snapshot model.ReceiveDrainSnapshot) {
	r.mu.Lock()
	proof := r.fanoutProof
	required := r.receiveDrainRequired
	r.mu.Unlock()
	if !required {
		return
	}
	snapshot = attachFanoutProof(snapshot, proof)
	r.mu.Lock()
	if r.receiveDrainRequired {
		r.receiveDrainTerminal = snapshot
		r.receiveDrainTerminalSet = true
	}
	r.mu.Unlock()
}

func trafficStatusFromMetrics(snapshot metrics.SnapshotData) TrafficStatus {
	status := TrafficStatus{
		Planned:           measuredCounterSum(snapshot, "workload_scheduler_planned_total"),
		Dispatched:        measuredCounterSum(snapshot, "workload_scheduler_dispatched_total"),
		LogicalSent:       measuredCounterSum(snapshot, "logical_sent_total"),
		SendAttempts:      measuredCounterSum(snapshot, "send_attempt_total"),
		SendACKs:          measuredCounterSum(snapshot, "sendack_success_total"),
		WarmupSendACKs:    exactPhaseCounterSum(snapshot, "sendack_success_total", "warmup"),
		TerminalErrors:    measuredCounterSum(snapshot, "logical_terminal_error_total"),
		CorrectnessErrors: measuredCounterSum(snapshot, "logical_correctness_error_total"),
		RetryAttempts:     measuredCounterSum(snapshot, "retry_attempt_total"),
		RetryExhausted:    measuredCounterSum(snapshot, "retry_exhausted_total"),
	}
	status.Remaining, _ = measuredExactUintGauge(snapshot, "logical_remaining")
	logicalIdentities := measuredCounterSum(snapshot, "logical_identity_total")
	attemptRecords := measuredCounterSum(snapshot, "attempt_record_total")
	mismatches := measuredCounterSum(snapshot, "client_msg_no_mismatch_total")
	configuredAttempts, configuredOK := measuredUniformUintGauge(snapshot, "configured_maximum_attempts")
	maximumObserved, observedOK := measuredMaximumUintGauge(snapshot, "maximum_observed_attempts")
	if configuredOK && configuredAttempts == model.TrafficRetryMaximumAttempts {
		status.MaximumRetriesPerMessage = model.TrafficRetryMaximumRetries
	}
	status.StableClientMsgNo = status.LogicalSent > 0 && logicalIdentities == status.LogicalSent &&
		attemptRecords == status.SendAttempts && mismatches == 0
	terminal, terminalOK := checkedAddUint64(status.SendACKs, status.TerminalErrors)
	attempts, attemptsOK := checkedAddUint64(status.LogicalSent, status.RetryAttempts)
	settled, settledOK := checkedAddUint64(terminal, status.Remaining)
	maximumRetryAttempts, maximumRetryOK := checkedMultiplyUint64(status.LogicalSent, model.TrafficRetryMaximumRetries)
	status.RetryEvidenceComplete = status.StableClientMsgNo && configuredOK && observedOK &&
		configuredAttempts == model.TrafficRetryMaximumAttempts && maximumObserved >= 1 &&
		maximumObserved <= model.TrafficRetryMaximumAttempts && terminalOK && settledOK &&
		settled == status.LogicalSent && attemptsOK && attempts == status.SendAttempts && maximumRetryOK &&
		status.RetryAttempts <= maximumRetryAttempts
	return status
}

func measuredCounterSum(snapshot metrics.SnapshotData, wanted string) uint64 {
	var total uint64
	for key, value := range snapshot.Counters {
		name, labels, err := metrics.ParseSeries(key)
		if err != nil || name != wanted || !measuredPhaseLabel(labels["phase"]) {
			continue
		}
		if next, ok := checkedAddUint64(total, value); ok {
			total = next
		} else {
			return ^uint64(0)
		}
	}
	return total
}

func exactPhaseCounterSum(snapshot metrics.SnapshotData, wanted, phase string) uint64 {
	var total uint64
	for key, value := range snapshot.Counters {
		name, labels, err := metrics.ParseSeries(key)
		if err != nil || name != wanted || labels["phase"] != phase {
			continue
		}
		if next, ok := checkedAddUint64(total, value); ok {
			total = next
		} else {
			return ^uint64(0)
		}
	}
	return total
}

func measuredExactUintGauge(snapshot metrics.SnapshotData, wanted string) (uint64, bool) {
	var total float64
	found := false
	for key, value := range snapshot.Gauges {
		name, labels, err := metrics.ParseSeries(key)
		if err != nil || name != wanted || !measuredPhaseLabel(labels["phase"]) {
			continue
		}
		found = true
		total += value
	}
	if !found || total < 0 || total > math.MaxUint64 || math.Trunc(total) != total {
		return 0, false
	}
	return uint64(total), true
}

func measuredUniformUintGauge(snapshot metrics.SnapshotData, wanted string) (uint64, bool) {
	var expected uint64
	found := false
	for key, value := range snapshot.Gauges {
		name, labels, err := metrics.ParseSeries(key)
		if err != nil || name != wanted || !measuredPhaseLabel(labels["phase"]) {
			continue
		}
		if value < 0 || value > math.MaxUint64 || math.Trunc(value) != value {
			return 0, false
		}
		current := uint64(value)
		if found && current != expected {
			return 0, false
		}
		expected = current
		found = true
	}
	return expected, found
}

func measuredMaximumUintGauge(snapshot metrics.SnapshotData, wanted string) (uint64, bool) {
	var maximum uint64
	found := false
	for key, value := range snapshot.Gauges {
		name, labels, err := metrics.ParseSeries(key)
		if err != nil || name != wanted || !measuredPhaseLabel(labels["phase"]) {
			continue
		}
		if value < 0 || value > math.MaxUint64 || math.Trunc(value) != value {
			return 0, false
		}
		current := uint64(value)
		if !found || current > maximum {
			maximum = current
		}
		found = true
	}
	return maximum, found
}

func measuredPhaseLabel(phase string) bool {
	return phase == "run" || strings.HasPrefix(phase, "run-window-")
}

func checkedAddUint64(left, right uint64) (uint64, bool) {
	if left > ^uint64(0)-right {
		return 0, false
	}
	return left + right, true
}

func checkedMultiplyUint64(left uint64, right int) (uint64, bool) {
	if right < 0 || (right != 0 && left > ^uint64(0)/uint64(right)) {
		return 0, false
	}
	return left * uint64(right), true
}

// ResetTraffic rebuilds traffic workloads while keeping the active sessions open.
func (r *defaultWorkloadRunner) ResetTraffic(assignment Assignment) error {
	r.maintenanceMu.Lock()
	defer r.maintenanceMu.Unlock()
	manager, err := r.managerForRun(assignment.RunID)
	if err != nil {
		return err
	}
	budget := assignment.Scenario.Run.Cooldown
	if budget <= 0 {
		budget = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	return r.rebuildTrafficFromManager(ctx, assignment, manager)
}

// RecoverTraffic repairs failed sessions and rebuilds workloads for the next traffic window.
func (r *defaultWorkloadRunner) RecoverTraffic(ctx context.Context, assignment Assignment, cause error) error {
	r.maintenanceMu.Lock()
	defer r.maintenanceMu.Unlock()
	manager, err := r.managerForRun(assignment.RunID)
	if err != nil {
		return err
	}
	if err := r.stopCurrentReceiveGeneration(ctx, assignment.RunID); err != nil {
		return markTargetUnavailable(err)
	}
	if err := r.repairSessions(ctx, assignment, manager, cause); err != nil {
		return markTargetUnavailable(err)
	}
	return r.rebuildTrafficFromManager(ctx, assignment, manager)
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

func (r *defaultWorkloadRunner) rebuildTrafficFromManager(ctx context.Context, assignment Assignment, manager *benchworkload.ConnectionManager) error {
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
		return r.replaceTrafficGeneration(ctx, assignment.RunID, manager, nil, nil, nil)
	}
	rawClients, err := personClientsFromManager(manager, users)
	if err != nil {
		return err
	}
	r.mu.Lock()
	proof := r.fanoutProof
	proofAssignmentID := r.fanoutProofAssignmentID
	r.mu.Unlock()
	if assignment.Scenario.Run.ExternalTerminalCut && (proof == nil || proofAssignmentID != assignment.AssignmentID) {
		return fmt.Errorf("worker runner: external terminal cut fanout proof is not bound to assignment")
	}
	clients := benchworkload.WrapPersonClientsForConcurrentReadsWithFanoutProof(rawClients, proof)
	workloads, err := buildPersonWorkloads(assignment, plan.bundles, clients)
	if err != nil {
		return err
	}
	groupWorkloads, err := buildGroupWorkloads(assignment, groupPlan.bundles, clients, proof)
	if err != nil {
		return err
	}
	var startAutoRecvAck func() *benchworkload.AutoRecvAckHandle
	if assignmentWantsRecvDrain(assignment) {
		startAutoRecvAck = func() *benchworkload.AutoRecvAckHandle {
			return benchworkload.StartAutoRecvAckHandleWithOptions(
				autoRecvAckClients(clients, plan.users, groupPlan.users, identityRangeUsers(assignment)),
				autoRecvAckOptionsForAssignment(assignment),
			)
		}
	}
	return r.replaceTrafficGeneration(ctx, assignment.RunID, manager, workloads, groupWorkloads, startAutoRecvAck)
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
	if err == nil {
		for _, gaugeName := range []string{"configured_maximum_attempts", "maximum_observed_attempts"} {
			for key := range aggregated.Gauges {
				name, _, parseErr := metrics.ParseSeries(key)
				if parseErr != nil || name != gaugeName {
					continue
				}
				var maximum float64
				for _, snapshot := range snapshots {
					if value := snapshot.Metrics.Gauges[key]; value > maximum {
						maximum = value
					}
				}
				aggregated.Gauges[key] = maximum
			}
		}
	}
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
	if assignment.Scenario.Run.Duration <= 0 {
		if assignment.Scenario.Online.Churn.Enabled {
			return r.runWithScheduledChurn(ctx, assignment)
		}
		return r.runPhaseWithIdleHold(ctx, assignment, 0, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
			if person != nil {
				return person.Run(phaseCtx)
			}
			return group.Run(phaseCtx)
		})
	}
	deadline := time.Now().Add(assignment.Scenario.Run.Duration)
	task, err := r.startMeasuredTrafficTask(assignment, deadline)
	if err != nil {
		return err
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		task.cancel()
		_ = task.wait()
		r.clearMeasuredTrafficTask(task)
		return ctx.Err()
	case <-timer.C:
		return nil
	case <-task.done:
		if task.err != nil {
			r.clearMeasuredTrafficTask(task)
			return task.err
		}
		if err := sleepContext(ctx, time.Until(deadline)); err != nil {
			r.clearMeasuredTrafficTask(task)
			return err
		}
		return nil
	}
}

func (r *defaultWorkloadRunner) startMeasuredTrafficTask(assignment Assignment, admissionDeadline time.Time) (*measuredTrafficTask, error) {
	r.mu.Lock()
	if r.runID != assignment.RunID {
		r.mu.Unlock()
		return nil, fmt.Errorf("worker runner: cannot start measured traffic for inactive run %q", assignment.RunID)
	}
	if r.measuredTask != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("worker runner: measured traffic is already active for run %q", assignment.RunID)
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	task := &measuredTrafficTask{runID: assignment.RunID, cancel: cancel, done: make(chan struct{})}
	r.measuredTask = task
	r.mu.Unlock()

	go func() {
		if assignment.Scenario.Online.Churn.Enabled {
			task.err = r.runWithScheduledChurnUntil(taskCtx, assignment, admissionDeadline)
		} else {
			task.err = r.runPhaseWithIdleHold(taskCtx, assignment, assignment.Scenario.Run.Duration, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
				if person != nil {
					return person.RunUntil(phaseCtx, admissionDeadline)
				}
				return group.RunUntil(phaseCtx, admissionDeadline)
			})
		}
		close(task.done)
	}()
	return task, nil
}

func (r *defaultWorkloadRunner) clearMeasuredTrafficTask(task *measuredTrafficTask) {
	r.mu.Lock()
	if r.measuredTask == task {
		r.measuredTask = nil
	}
	r.mu.Unlock()
}

func (r *defaultWorkloadRunner) currentMeasuredTrafficTask(runID string) *measuredTrafficTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runID != runID || r.measuredTask == nil || r.measuredTask.runID != runID {
		return nil
	}
	return r.measuredTask
}

func (r *defaultWorkloadRunner) runWithScheduledChurn(ctx context.Context, assignment Assignment) error {
	return r.runWithScheduledChurnUntil(ctx, assignment, time.Time{})
}

func (r *defaultWorkloadRunner) runWithScheduledChurnUntil(ctx context.Context, assignment Assignment, admissionDeadline time.Time) error {
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
		if !admissionDeadline.IsZero() && !time.Now().Before(admissionDeadline) {
			return nil
		}
		window := min(remaining, churn.Interval)
		windowDeadline := time.Now().Add(window)
		if !admissionDeadline.IsZero() && admissionDeadline.Before(windowDeadline) {
			windowDeadline = admissionDeadline
		}
		if err := r.runPhaseWithIdleHold(ctx, assignment, window, func(phaseCtx context.Context, person *benchworkload.PersonWorkload, group *benchworkload.GroupWorkload) error {
			if person != nil {
				return person.RunMeasuredWindowUntil(phaseCtx, window, cycle, windowDeadline)
			}
			return group.RunMeasuredWindowUntil(phaseCtx, window, cycle, windowDeadline)
		}); err != nil {
			return err
		}
		remaining -= window
		if remaining <= 0 || (!admissionDeadline.IsZero() && !time.Now().Before(admissionDeadline)) {
			return nil
		}
		maintenanceCtx := ctx
		cancelMaintenance := func() {}
		if !admissionDeadline.IsZero() {
			maintenanceCtx, cancelMaintenance = context.WithDeadline(ctx, admissionDeadline)
		}
		err := r.applyScheduledChurn(maintenanceCtx, &assignment, cycle, swapGenerations)
		cancelMaintenance()
		if err != nil {
			return markTargetUnavailable(err)
		}
	}
	return nil
}

func (r *defaultWorkloadRunner) applyScheduledChurn(ctx context.Context, assignment *Assignment, cycle int, swapGenerations []int) error {
	r.maintenanceMu.Lock()
	defer r.maintenanceMu.Unlock()
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
	if err := prepareChurnGroupSubscriberSwaps(ctx, *assignment, cycle, replacements); err != nil {
		return err
	}
	if err := r.stopCurrentReceiveGeneration(ctx, assignment.RunID); err != nil {
		return err
	}
	if err := manager.ReconnectUsers(ctx, sameUsers); err != nil {
		return err
	}
	for _, item := range replacements {
		if _, err := manager.ReplaceUser(ctx, item.oldUID, item.user); err != nil {
			return err
		}
		assignment.Plan.OnlineIdentityIndexes[item.offset] = item.identityIndex
	}
	if err := r.rebuildTrafficFromManager(ctx, *assignment, manager); err != nil {
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
	budget := assignment.Scenario.Run.Cooldown
	if budget <= 0 {
		snapshot := r.receiveDrainSnapshot()
		r.storeReceiveDrainTerminal(snapshot)
		if task := r.currentMeasuredTrafficTask(assignment.RunID); task != nil {
			task.cancel()
			_ = task.wait()
			r.clearMeasuredTrafficTask(task)
			return fmt.Errorf("worker runner: measured traffic did not drain before zero cooldown deadline")
		}
		if !snapshot.TerminalProofComplete() {
			return fmt.Errorf("worker runner: receive drain did not converge before zero cooldown deadline")
		}
		if assignment.Scenario.Run.ExternalTerminalCut {
			if !snapshot.FanoutProof.Required || !snapshot.FanoutProof.Complete() {
				return fmt.Errorf("worker runner: fanout proof is incomplete")
			}
			return fmt.Errorf("worker runner: external terminal cut did not complete before zero cooldown deadline")
		}
		return nil
	}
	drainCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	task := r.currentMeasuredTrafficTask(assignment.RunID)
	if task != nil {
		select {
		case <-task.done:
			r.clearMeasuredTrafficTask(task)
			if task.err != nil {
				r.storeReceiveDrainTerminal(r.receiveDrainSnapshot())
				return task.err
			}
		case <-drainCtx.Done():
			completed, taskErr := task.completed()
			if !completed {
				task.cancel()
				taskErr = task.wait()
			}
			r.clearMeasuredTrafficTask(task)
			if taskErr == nil {
				break
			}
			r.storeReceiveDrainTerminal(r.receiveDrainSnapshot())
			if completed {
				return taskErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("worker runner: measured traffic drain exceeded %s", budget)
		}
	}

	r.mu.Lock()
	required := r.receiveDrainRequired
	handle := r.autoRecvAck
	r.mu.Unlock()
	if !required {
		snapshot := model.ReceiveDrainNotRequired()
		r.storeReceiveDrainTerminal(snapshot)
		return r.terminalCut.wait(drainCtx, assignment)
	}
	if handle == nil {
		snapshot := model.ReceiveDrainSnapshot{Required: true}
		r.storeReceiveDrainTerminal(snapshot)
		return fmt.Errorf("worker runner: receive drain evidence unavailable")
	}
	snapshot, err := handle.WaitDrained(drainCtx)
	snapshot = r.mergeReceiveDrainHistory(snapshot)
	r.storeReceiveDrainTerminal(snapshot)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("worker runner: receive drain exceeded %s", budget)
		}
		return fmt.Errorf("worker runner: receive drain failed: %w", err)
	}
	if assignment.Scenario.Run.ExternalTerminalCut {
		r.mu.Lock()
		manager := r.manager
		requiredConnections := r.terminalCutRequiredConnections
		prepare := r.terminalFencePrepare
		r.mu.Unlock()
		if manager == nil || requiredConnections <= 0 || manager.ActiveCount() != requiredConnections {
			return fmt.Errorf("worker runner: terminal fence session coverage changed")
		}
		if prepare == nil {
			return fmt.Errorf("worker runner: target terminal fence preparation unavailable")
		}
		grant, prepareErr := prepare(drainCtx, assignment, requiredConnections)
		if prepareErr != nil {
			return prepareErr
		}
		if err := manager.SealIngressWithFence(drainCtx, grant); err != nil {
			return fmt.Errorf("worker runner: establish terminal session fence: %w", err)
		}
		// The decoded ACK orders all pre-fence RECV frames before this cut. Rebuild
		// temporal zero evidence after every client has acknowledged the epoch.
		snapshot, err = handle.WaitDrained(drainCtx)
		snapshot = r.mergeReceiveDrainHistory(snapshot)
		snapshot = attachFanoutProof(snapshot, r.currentFanoutProof(assignment))
		r.storeReceiveDrainTerminal(snapshot)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("worker runner: post-fence receive drain failed: %w", err)
		}
		if !snapshot.FanoutProof.Required || !snapshot.FanoutProof.Complete() {
			return fmt.Errorf("worker runner: fanout proof is incomplete")
		}
	}
	return r.terminalCut.wait(drainCtx, assignment)
}

func (r *defaultWorkloadRunner) currentFanoutProof(assignment Assignment) *benchworkload.GroupFanoutProof {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runID != assignment.RunID || r.fanoutProofAssignmentID != assignment.AssignmentID {
		return nil
	}
	return r.fanoutProof
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
			RetryEnabled:     bundle.traffic.Retry.Enabled,
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

func (r *defaultWorkloadRunner) replaceTrafficGeneration(ctx context.Context, runID string, manager *benchworkload.ConnectionManager, personWorkloads []*benchworkload.PersonWorkload, groupWorkloads []*benchworkload.GroupWorkload, startAutoRecvAck func() *benchworkload.AutoRecvAckHandle) error {
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
	if ctx == nil {
		ctx = context.Background()
	}
	if previousAutoRecvAck != nil {
		drained, stopped, err := previousAutoRecvAck.DrainAndStop(ctx)
		r.archiveReceiveDrainGeneration(drained, stopped, err != nil)
		if err != nil {
			return fmt.Errorf("worker runner: drain previous receive generation: %w", err)
		}
	}
	if previousManager != nil && previousManager != manager {
		_ = previousManager.Close()
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
	if autoRecvAck != nil {
		r.receiveDrainRequired = true
	}
	r.receiveDrainTerminal = model.ReceiveDrainSnapshot{}
	r.receiveDrainTerminalSet = false
	r.mu.Unlock()
	return nil
}

func (r *defaultWorkloadRunner) stopCurrentReceiveGeneration(ctx context.Context, runID string) error {
	r.replaceMu.Lock()
	defer r.replaceMu.Unlock()

	r.mu.Lock()
	if r.runID != runID {
		r.mu.Unlock()
		return fmt.Errorf("worker runner: cannot drain traffic for inactive run %q", runID)
	}
	previous := r.autoRecvAck
	r.autoRecvAck = nil
	r.mu.Unlock()
	if previous == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	drained, stopped, err := previous.DrainAndStop(ctx)
	r.archiveReceiveDrainGeneration(drained, stopped, err != nil)
	if err != nil {
		return fmt.Errorf("worker runner: drain previous receive generation: %w", err)
	}
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
	if task := r.currentMeasuredTrafficTask(runID); task != nil {
		task.cancel()
		_ = task.wait()
		r.clearMeasuredTrafficTask(task)
	}
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
	if r.fanoutProof == nil || r.terminalSealedLifecycle != nil {
		r.fanoutProof = nil
		r.fanoutProofAssignmentID = ""
	}
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
	r.fanoutProof = nil
	r.fanoutProofAssignmentID = ""
	r.archivedWorkloadMetrics = nil
	r.metrics = metrics.NewRegistry()
	r.receiveDrainRequired = false
	r.receiveDrainTerminal = model.ReceiveDrainSnapshot{}
	r.receiveDrainTerminalSet = false
	r.receiveDrainReadFailures = 0
	r.receiveDrainACKFailures = 0
	r.receiveDrainACKSuccesses = 0
	r.receiveDrainFramesObserved = 0
	r.receiveDrainFramesDrained = 0
	r.receiveDrainEvidenceLost = false
	r.terminalCutRequiredConnections = 0
	r.terminalSealedLifecycle = nil
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
