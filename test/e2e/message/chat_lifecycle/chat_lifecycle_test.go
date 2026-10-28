//go:build e2e

package chat_lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	benchlifecycle "github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	benchToken        = "chat-lifecycle-e2e-bench-token"
	operationTimeout  = 20 * time.Second
	idleEvictAfter    = 5 * time.Minute
	idlePollDeadline  = 6*time.Minute + 15*time.Second
	coldProofMaxPolls = 3
	coldProofMaxReads = 120
)

func TestPersonChannelNaturalReheat(t *testing.T) {
	report := newRuntimeActivityReport(t)
	cluster := startLifecycleCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute+30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	api := lifecycleTarget(cluster)
	baseline := requireCreatedTotal(t, ctx, cluster)

	const (
		leftUID  = "e2e-lifecycle-left"
		rightUID = "e2e-lifecycle-right"
	)
	canonicalChannel := channelid.EncodePersonChannel(leftUID, rightUID)
	probeRequest := model.ChannelRuntimeProbeRequest{Channels: []model.ChannelRuntimeChannelIdentity{{
		ChannelID: canonicalChannel, ChannelType: uint8(frame.ChannelTypePerson),
	}}}

	leftWire, rightWire := newAckWireProbe(), newAckWireProbe()
	left := connectAndFullSync(t, ctx, cluster, api, 1, leftUID, leftWire)
	right := connectAndFullSync(t, ctx, cluster, api, 2, rightUID, rightWire)
	first := sendPersonMessage(t, cluster, left, right, rightUID, 1, "e2e-lifecycle-first")

	requireManagerRuntimeActivity(t, ctx, cluster, canonicalChannel, "initial", "active", first, report)
	initial := requireLoadedLeader(t, ctx, cluster, api, probeRequest, first)
	report.Probes = append(report.Probes, captureRuntimeActivityProbe("initial", initial, first))
	require.Equal(t, baseline+1, requireCreatedTotalEventually(t, ctx, cluster, baseline+1))
	require.NoError(t, left.Close())
	require.NoError(t, right.Close())

	quietStarted := time.Now()
	requireColdBoundaryProof(t, ctx, cluster, api, canonicalChannel, first, report.ManagerReads[0].SlotID, quietStarted, report)
	report.NaturalIdleSeconds = time.Since(quietStarted).Seconds()
	for range 3 {
		requireManagerRuntimeActivity(t, ctx, cluster, canonicalChannel, "idle", "inactive", first, report)
		missing, probeErr := api.ProbeChannelRuntimeAll(ctx, probeRequest)
		report.Probes = append(report.Probes, captureRuntimeActivityProbe("after_manager_reads", missing, first))
		require.NoError(t, probeErr, cluster.DumpDiagnostics())
		require.True(t, missingEvidenceValid(missing), "Manager reads must not wake the cold Channel: %s", summarizeProbe(missing))
	}

	left = connectAndFullSyncContaining(t, ctx, cluster, api, 1, leftUID, "e2e-lifecycle-first")
	defer func() { _ = left.Close() }()
	right = connectAndFullSyncContaining(t, ctx, cluster, api, 2, rightUID, "e2e-lifecycle-first")
	defer func() { _ = right.Close() }()
	second := sendPersonMessage(t, cluster, left, right, rightUID, 2, "e2e-lifecycle-second")
	report.ReheatSequence = second
	require.Greater(t, second, first, cluster.DumpDiagnostics())

	reheated := requireLoadedLeader(t, ctx, cluster, api, probeRequest, second)
	report.Probes = append(report.Probes, captureRuntimeActivityProbe("reheated", reheated, second))
	requireManagerRuntimeActivity(t, ctx, cluster, canonicalChannel, "reheated", "active", second, report)
	requireMonotonicRuntimeEvidence(t, initial, reheated)
	require.Equal(t, baseline+1, requireCreatedTotalEventually(t, ctx, cluster, baseline+1),
		"reheat must reuse replicated Channel metadata")
}

// runtimeActivityReport retains bounded public evidence without Channel or user identities.
type runtimeActivityReport struct {
	Scenario           string                    `json:"scenario"`
	StartedAt          time.Time                 `json:"started_at"`
	FinishedAt         time.Time                 `json:"finished_at"`
	Passed             bool                      `json:"passed"`
	Nodes              int                       `json:"nodes"`
	HashSlots          int                       `json:"hash_slots"`
	IdleThresholdSecs  int                       `json:"idle_threshold_seconds"`
	NaturalIdleSeconds float64                   `json:"natural_idle_seconds"`
	ReheatSequence     uint64                    `json:"product_reheat_sequence"`
	ColdProof          *runtimeActivityColdProof `json:"cold_boundary_proof,omitempty"`
	ManagerReads       []runtimeActivityRead     `json:"manager_reads"`
	Probes             []runtimeActivityProbe    `json:"runtime_probes"`
}

type runtimeActivityRead struct {
	Phase         string  `json:"phase"`
	Query         string  `json:"query"`
	IngressNodeID uint64  `json:"ingress_node_id"`
	SlotID        uint32  `json:"slot_id"`
	Lifecycle     string  `json:"lifecycle_status"`
	RuntimeStatus string  `json:"runtime_status"`
	RuntimeNodeID uint64  `json:"runtime_node_id"`
	Leader        uint64  `json:"leader"`
	MinISR        int64   `json:"min_isr"`
	MaxMessageSeq *uint64 `json:"max_message_seq"`
}

type runtimeActivityProbe struct {
	Phase        string                     `json:"phase"`
	LeaderLoaded bool                       `json:"leader_loaded"`
	Missing      bool                       `json:"missing_on_all_nodes"`
	Nodes        []runtimeActivityProbeNode `json:"nodes"`
}

type runtimeActivityProbeNode struct {
	NodeID uint64 `json:"node_id"`
	Role   string `json:"role"`
	Status string `json:"status"`
	LEO    uint64 `json:"leo"`
	HW     uint64 `json:"hw"`
}

// runtimeActivityColdProof describes one E2E bridge, not a worker timer lease
// or the 1,200-candidate ProductionLifecycle.Run protocol.
type runtimeActivityColdProof struct {
	Scope           string                     `json:"scope"`
	BoundarySource  string                     `json:"boundary_source"`
	QuietStartedAt  time.Time                  `json:"quiet_started_at"`
	QuietNotBefore  time.Time                  `json:"quiet_not_before"`
	QuietDeadline   time.Time                  `json:"quiet_deadline"`
	InitialSequence uint64                     `json:"initial_sequence"`
	Polls           []runtimeActivityProofPoll `json:"polls"`
}

type runtimeActivityProofPoll struct {
	Phase           string                                   `json:"phase"`
	StartedAt       time.Time                                `json:"started_at"`
	FinishedAt      time.Time                                `json:"finished_at"`
	ProbeStartedAt  time.Time                                `json:"probe_started_at"`
	ProbeFinishedAt time.Time                                `json:"probe_finished_at"`
	Succeeded       bool                                     `json:"succeeded"`
	Batch           benchlifecycle.LifecycleProbeBatchResult `json:"batch"`
	Snapshot        benchlifecycle.LifecycleProofSnapshot    `json:"snapshot"`
	Reads           []runtimeActivityProofRead               `json:"public_reads"`
}

type runtimeActivityProofRead struct {
	StartedAt  time.Time            `json:"started_at"`
	FinishedAt time.Time            `json:"finished_at"`
	Succeeded  bool                 `json:"succeeded"`
	Probe      runtimeActivityProbe `json:"probe"`
}

// lifecycleWallClock records the two actual Poll cuts without altering time.
// This one-candidate bridge admits one RPC batch and retains only two cuts.
type lifecycleWallClock struct {
	cuts  [2]time.Time
	count int
}

func (c *lifecycleWallClock) Now() time.Time {
	now := time.Now()
	if c.count < len(c.cuts) {
		c.cuts[c.count] = now
	}
	c.count++
	return now
}

func (*lifecycleWallClock) NewTicker(period time.Duration) benchlifecycle.ObserverTicker {
	return lifecycleWallTicker{time.NewTicker(period)}
}

type lifecycleWallTicker struct{ *time.Ticker }

func (t lifecycleWallTicker) C() <-chan time.Time { return t.Ticker.C }

// delayedLifecycleProber delays only the test caller. Every retained row comes
// unchanged from the real target Client's public all-three-node runtime probe.
type delayedLifecycleProber struct {
	api           *target.Client
	readNotBefore time.Time
	awaitMissing  bool
	committedSeq  uint64
	reads         []runtimeActivityProofRead
}

func (p *delayedLifecycleProber) ProbeChannelRuntimeAll(ctx context.Context, request model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	if err := waitLifecycleUntil(ctx, p.readNotBefore); err != nil {
		return nil, err
	}
	for range coldProofMaxReads {
		started := time.Now()
		rows, err := p.api.ProbeChannelRuntimeAll(ctx, request)
		p.reads = append(p.reads, runtimeActivityProofRead{
			StartedAt: started, FinishedAt: time.Now(), Succeeded: err == nil,
			Probe: captureRuntimeActivityProbe("proof_read", rows, p.committedSeq),
		})
		if err != nil || !p.awaitMissing || missingEvidenceValid(rows) {
			return rows, err
		}
		if err := waitLifecycleUntil(ctx, time.Now().Add(250*time.Millisecond)); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("cold-boundary public read limit exceeded")
}

func waitLifecycleUntil(ctx context.Context, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay := time.Until(at); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func requireColdBoundaryProof(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, api *target.Client, channelID string, sequence uint64, slotID uint32, quietStarted time.Time, report *runtimeActivityReport) {
	t.Helper()
	notBefore := quietStarted.Add(idleEvictAfter)
	deadline := notBefore.Add(time.Minute)
	report.ColdProof = &runtimeActivityColdProof{
		Scope:          "single_candidate_real_rpc_cold_boundary_bridge",
		BoundarySource: "test_after_sendack_recvack_disconnect_not_server_timestamp",
		QuietStartedAt: quietStarted, QuietNotBefore: notBefore, QuietDeadline: deadline,
		InitialSequence: sequence,
	}
	// Token/version and the unused reheat time satisfy the candidate shape only.
	// This bridge never approves a worker timer or advances proof reheat phases.
	proof, err := benchlifecycle.NewLifecycleProof([]benchlifecycle.LifecycleCandidate{{
		ChannelID: channelID, ChannelType: uint8(frame.ChannelTypePerson),
		HashSlot: uint16(crc32.ChecksumIEEE([]byte(channelID)) % 256), SlotID: slotID,
		TimerToken: 1, ActivityVersion: 1, InitialSequence: sequence,
		QuietNotBefore: notBefore, QuietDeadline: deadline, ReheatAt: deadline.Add(30 * time.Second), ObservedLoaded: true,
	}})
	require.NoError(t, err)
	loaded := requireColdBoundaryPoll(t, ctx, cluster, proof, &delayedLifecycleProber{api: api, committedSeq: sequence}, "loaded", report.ColdProof)
	require.Equal(t, uint64(1), loaded.Snapshot.Loaded)
	require.Zero(t, loaded.Snapshot.ColdEligible)

	// N is a conservative test boundary after the physical SEND. Do not assert
	// premature absence before N or infer a server last-activity timestamp.
	require.NoError(t, waitLifecycleUntil(ctx, notBefore.Add(-2*time.Second)))
	crossing := requireColdBoundaryPoll(t, ctx, cluster, proof, &delayedLifecycleProber{
		api: api, readNotBefore: notBefore.Add(2 * time.Second), awaitMissing: true, committedSeq: sequence,
	}, "crossing_quiet_boundary", report.ColdProof)
	require.True(t, crossing.ProbeStartedAt.Before(notBefore), "probe must actually start before the test boundary")
	require.False(t, crossing.ProbeFinishedAt.Before(notBefore), "probe must actually finish after the test boundary")
	require.False(t, crossing.Reads[0].StartedAt.Before(notBefore.Add(2*time.Second)), "public reads must follow the real boundary delay")
	require.True(t, crossing.Reads[len(crossing.Reads)-1].Probe.Missing, "crossing probe must contain real all-node absence")
	require.Zero(t, crossing.Snapshot.ProductFailureReasons.PrematureAbsence)
	require.Zero(t, crossing.Snapshot.ColdEligible, "a crossing interval cannot approve cold eligibility")
	require.LessOrEqual(t, time.Since(quietStarted), idlePollDeadline, "retain the original natural-idle deadline")

	fresh := requireColdBoundaryPoll(t, ctx, cluster, proof, &delayedLifecycleProber{api: api, committedSeq: sequence}, "fresh_after_quiet_boundary", report.ColdProof)
	require.False(t, fresh.ProbeStartedAt.Before(notBefore))
	require.False(t, fresh.ProbeFinishedAt.After(deadline))
	require.True(t, fresh.Reads[len(fresh.Reads)-1].Probe.Missing)
	require.Equal(t, uint64(1), fresh.Snapshot.ColdEligible)
	require.True(t, proof.ColdEligible(channelID))
	require.Zero(t, fresh.Snapshot.Reheated)
	require.Zero(t, fresh.Snapshot.Completed, "product SEND reheat is verified separately by the original scenario")
}

func requireColdBoundaryPoll(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, proof *benchlifecycle.LifecycleProof, prober *delayedLifecycleProber, phase string, report *runtimeActivityColdProof) runtimeActivityProofPoll {
	t.Helper()
	require.Less(t, len(report.Polls), coldProofMaxPolls, "cold proof poll limit")
	clock := &lifecycleWallClock{}
	poll := runtimeActivityProofPoll{Phase: phase, StartedAt: time.Now()}
	result, err := proof.Poll(ctx, prober, clock, benchlifecycle.LifecycleProbeOptions{
		BatchSize: 1, MaxConcurrency: 1, RequestTimeout: 30 * time.Second,
	})
	poll.FinishedAt, poll.Succeeded, poll.Batch = time.Now(), err == nil, result
	poll.ProbeStartedAt, poll.ProbeFinishedAt = clock.cuts[0], clock.cuts[1]
	poll.Reads, poll.Snapshot = prober.reads, proof.Snapshot()
	report.Polls = append(report.Polls, poll)
	require.NoError(t, err, "%s\n%s", phase, cluster.DumpDiagnostics())
	require.Equal(t, 2, clock.count, "one batch must retain exactly two real clock cuts")
	require.NotEmpty(t, poll.Reads)
	require.LessOrEqual(t, len(poll.Reads), coldProofMaxReads)
	for _, read := range poll.Reads {
		require.True(t, read.Succeeded)
		require.False(t, read.StartedAt.Before(poll.ProbeStartedAt))
		require.False(t, read.FinishedAt.Before(read.StartedAt))
		require.False(t, poll.ProbeFinishedAt.Before(read.FinishedAt))
	}
	require.Equal(t, uint64(1), result.Requests)
	require.Equal(t, uint64(3), result.Nodes)
	require.Zero(t, result.TransportErrors)
	require.Zero(t, poll.Snapshot.ProductFailures)
	require.Zero(t, poll.Snapshot.HarnessFailures)
	return poll
}

func captureRuntimeActivityProbe(phase string, results []model.ChannelRuntimeProbeResult, committedSeq uint64) runtimeActivityProbe {
	probe := runtimeActivityProbe{
		Phase: phase, LeaderLoaded: loadedLeaderEvidenceValid(results, committedSeq), Missing: missingEvidenceValid(results),
	}
	for _, result := range results {
		if len(result.Channels) == 1 {
			row := result.Channels[0]
			probe.Nodes = append(probe.Nodes, runtimeActivityProbeNode{
				NodeID: result.NodeID, Role: row.Role, Status: row.Status, LEO: row.LEO, HW: row.HW,
			})
		}
	}
	return probe
}

func newRuntimeActivityReport(t *testing.T) *runtimeActivityReport {
	t.Helper()
	path := os.Getenv("WK_E2E_CHAT_LIFECYCLE_REPORT")
	if path == "" {
		dir, err := os.MkdirTemp("", "wk-channel-runtime-activity-")
		require.NoError(t, err)
		path = filepath.Join(dir, "natural-reheat.json")
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	report := &runtimeActivityReport{
		Scenario: "person_channel_natural_reheat", StartedAt: time.Now().UTC(),
		Nodes: 3, HashSlots: 256, IdleThresholdSecs: int(idleEvictAfter.Seconds()),
	}
	t.Cleanup(func() {
		report.FinishedAt, report.Passed = time.Now().UTC(), !t.Failed()
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(path, append(data, '\n'), 0o600)
		}
		if err != nil {
			t.Errorf("write runtime activity report: %v", err)
		}
		t.Logf("Channel runtime activity report: %s", path)
	})
	return report
}

// requireManagerRuntimeActivity exercises the same paged list as the Manager UI
// and its exact lookup on every ingress, including a request for committed head.
func requireManagerRuntimeActivity(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	channelID, phase, runtimeStatus string,
	messageSeq uint64,
	report *runtimeActivityReport,
) {
	t.Helper()
	type activityRow struct {
		suite.ChannelRuntimeMeta
		RuntimeStatus string `json:"runtime_status"`
		RuntimeNodeID uint64 `json:"runtime_node_id"`
	}
	for _, node := range cluster.Nodes {
		for _, queryKind := range []string{"list", "exact"} {
			query := url.Values{"limit": {"100"}, "include_max_message_seq": {"true"}}
			if queryKind == "exact" {
				query.Set("exact", "1")
				query.Set("channel_id", channelID)
				query.Set("channel_type", fmt.Sprint(uint8(frame.ChannelTypePerson)))
			}
			var page struct {
				Items []activityRow `json:"items"`
			}
			requestCtx, cancel := context.WithTimeout(ctx, operationTimeout)
			_, err := suite.GetJSON(requestCtx, "http://"+node.ManagerAddr()+"/manager/channel-runtime-meta?"+query.Encode(), &page)
			cancel()
			require.NoError(t, err, "%s %s on node %d", phase, queryKind, node.Spec.ID)
			var found *activityRow
			for i := range page.Items {
				if page.Items[i].ChannelID == channelID && page.Items[i].ChannelType == int64(frame.ChannelTypePerson) {
					found = &page.Items[i]
					break
				}
			}
			require.NotNil(t, found, "Channel is missing from %s %s on node %d", phase, queryKind, node.Spec.ID)
			report.ManagerReads = append(report.ManagerReads, runtimeActivityRead{
				Phase: phase, Query: queryKind, IngressNodeID: node.Spec.ID, SlotID: found.SlotID,
				Lifecycle: found.Status, RuntimeStatus: found.RuntimeStatus, RuntimeNodeID: found.RuntimeNodeID,
				Leader: found.Leader, MinISR: found.MinISR, MaxMessageSeq: found.MaxMessageSeq,
			})
			require.Equal(t, "active", found.Status, "idle eviction must preserve the Channel lifecycle")
			require.Equal(t, runtimeStatus, found.RuntimeStatus, "%s %s on node %d", phase, queryKind, node.Spec.ID)
			require.NotZero(t, found.Leader)
			require.Equal(t, found.Leader, found.RuntimeNodeID, "activity must describe the authoritative Channel Leader")
			require.Len(t, found.Replicas, 3, "retain the three-node replicated Channel topology")
			require.Equal(t, int64(2), found.MinISR, "exercise the replicated cold-read activation regression")
			require.NotNil(t, found.MaxMessageSeq)
			require.Equal(t, messageSeq, *found.MaxMessageSeq, "Manager must retain the committed head across idle eviction")
		}
	}
}

func TestPersonChannelCrossIngressBurstPreservesReceiveSequence(t *testing.T) {
	cluster := startLifecycleCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	api := lifecycleTarget(cluster)
	const (
		leftUID        = "e2e-sequence-left"
		rightUID       = "e2e-sequence-right"
		messagesPerWay = 100
		rounds         = 6
	)
	leftWire, rightWire := newAckWireProbe(), newAckWireProbe()
	left := connectAndFullSync(t, ctx, cluster, api, 1, leftUID, leftWire)
	defer func() { _ = left.Close() }()
	right := connectAndFullSync(t, ctx, cluster, api, 2, rightUID, rightWire)
	defer func() { _ = right.Close() }()

	var leftLast, rightLast uint64
	for round := 0; round < rounds; round++ {
		start := round*messagesPerWay + 1
		var sends sync.WaitGroup
		sendErrs := make(chan error, 2)
		sends.Add(2)
		go func() {
			defer sends.Done()
			sendErrs <- sendPersonBurst(left, rightUID, "left", start, messagesPerWay)
		}()
		go func() {
			defer sends.Done()
			sendErrs <- sendPersonBurst(right, leftUID, "right", start, messagesPerWay)
		}()
		sends.Wait()
		close(sendErrs)
		for sendErr := range sendErrs {
			require.NoError(t, sendErr, cluster.DumpDiagnostics())
		}

		requireSuccessfulSendacks(t, cluster, left, leftWire, start, messagesPerWay)
		requireSuccessfulSendacks(t, cluster, right, rightWire, start, messagesPerWay)
		leftLast = requireMonotonicPersonReceives(t, cluster, left, rightUID, "right", start, messagesPerWay, leftLast)
		rightLast = requireMonotonicPersonReceives(t, cluster, right, leftUID, "left", start, messagesPerWay, rightLast)
	}
	t.Logf(`WKRC-SESSION-ORDER {"scenario":"person","messages":%d,"ack_order":true,"sender_order":true,"receive_sequence":true}`, messagesPerWay*rounds*2)
}

func TestGroupChannelCrossIngressBurstPreservesReceiveSequence(t *testing.T) {
	cluster := startLifecycleCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	const (
		channelID      = "e2e-sequence-group"
		leftUID        = "e2e-sequence-group-left"
		rightUID       = "e2e-sequence-group-right"
		recipientUID   = "e2e-sequence-group-recipient"
		messagesPerWay = 100
		rounds         = 6
	)
	require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"reset": 1, "subscribers": []string{leftUID, rightUID, recipientUID},
	}), cluster.DumpDiagnostics())

	api := lifecycleTarget(cluster)
	leftWire, rightWire := newAckWireProbe(), newAckWireProbe()
	left := connectAndFullSync(t, ctx, cluster, api, 1, leftUID, leftWire)
	defer func() { _ = left.Close() }()
	right := connectAndFullSync(t, ctx, cluster, api, 2, rightUID, rightWire)
	defer func() { _ = right.Close() }()
	recipient := connectAndFullSync(t, ctx, cluster, api, 3, recipientUID)
	defer func() { _ = recipient.Close() }()

	var previous uint64
	for round := 0; round < rounds; round++ {
		start := round*messagesPerWay + 1
		var sends sync.WaitGroup
		sendErrs := make(chan error, 2)
		sends.Add(2)
		go func() {
			defer sends.Done()
			sendErrs <- sendGroupBurst(left, channelID, "left", start, messagesPerWay)
		}()
		go func() {
			defer sends.Done()
			sendErrs <- sendGroupBurst(right, channelID, "right", start, messagesPerWay)
		}()
		sends.Wait()
		close(sendErrs)
		for sendErr := range sendErrs {
			require.NoError(t, sendErr, cluster.DumpDiagnostics())
		}

		requireSuccessfulSendacks(t, cluster, left, leftWire, start, messagesPerWay)
		requireSuccessfulSendacks(t, cluster, right, rightWire, start, messagesPerWay)
		previous = requireMonotonicGroupReceives(t, cluster, recipient, channelID, map[string]string{leftUID: "left", rightUID: "right"}, start, messagesPerWay*2, previous)
	}
	t.Logf(`WKRC-SESSION-ORDER {"scenario":"group","messages":%d,"ack_order":true,"sender_order":true,"receive_sequence":true}`, messagesPerWay*rounds*2)
}

func sendPersonBurst(
	client *suite.WKProtoClient,
	peerUID, prefix string,
	start, count int,
) error {
	for offset := 0; offset < count; offset++ {
		ordinal := start + offset
		if err := client.SendFrame(&frame.SendPacket{
			ChannelID: peerUID, ChannelType: frame.ChannelTypePerson,
			ClientSeq: uint64(ordinal), ClientMsgNo: fmt.Sprintf("e2e-sequence-%s-%06d", prefix, ordinal),
			Payload: []byte("cross-ingress sequence evidence"),
		}); err != nil {
			return err
		}
	}
	return nil
}

func sendGroupBurst(client *suite.WKProtoClient, channelID, prefix string, start, count int) error {
	for offset := 0; offset < count; offset++ {
		ordinal := start + offset
		if err := client.SendFrame(&frame.SendPacket{
			ChannelID: channelID, ChannelType: frame.ChannelTypeGroup,
			ClientSeq: uint64(ordinal), ClientMsgNo: fmt.Sprintf("e2e-sequence-group-%s-%06d", prefix, ordinal),
			Payload: []byte("cross-ingress group sequence evidence"),
		}); err != nil {
			return err
		}
	}
	return nil
}

func requireSuccessfulSendacks(t *testing.T, cluster *suite.StartedCluster, client *suite.WKProtoClient, wire *ackWireProbe, start, count int) {
	t.Helper()
	var previous uint64
	for offset := range count {
		// Future-completion goroutines may reorder the bridge; only the TCP
		// capture proves wire ACK order. Still drain and check every future.
		futureAck, err := client.ReadSendAck()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, frame.ReasonSuccess, futureAck.ReasonCode)
		var ack frame.SendackPacket
		select {
		case ack = <-wire.acks:
		case <-time.After(operationTimeout):
			t.Fatal("wire SENDACK not observed")
		}
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
		require.Equal(t, uint64(start+offset), ack.ClientSeq, "session ACK order")
		require.Greater(t, ack.MessageSeq, previous, "same-sender committed sequence")
		previous = ack.MessageSeq
	}
}

func requireMonotonicPersonReceives(
	t *testing.T,
	cluster *suite.StartedCluster,
	client *suite.WKProtoClient,
	peerUID, prefix string,
	start, count int,
	previous uint64,
) uint64 {
	t.Helper()
	for offset := range count {
		recv, err := client.ReadRecv()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, peerUID, recv.FromUID, cluster.DumpDiagnostics())
		require.Equal(t, fmt.Sprintf("e2e-sequence-%s-%06d", prefix, start+offset), recv.ClientMsgNo, "person sender input order")
		require.Greater(t, recv.MessageSeq, previous,
			"person-channel receive sequence regressed from %d to %d\n%s", previous, recv.MessageSeq, cluster.DumpDiagnostics())
		require.NoError(t, client.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
		previous = recv.MessageSeq
	}
	return previous
}

func requireMonotonicGroupReceives(
	t *testing.T,
	cluster *suite.StartedCluster,
	client *suite.WKProtoClient,
	channelID string,
	senders map[string]string,
	start, count int,
	previous uint64,
) uint64 {
	t.Helper()
	next := make(map[string]int, len(senders))
	for uid := range senders {
		next[uid] = start
	}
	for range count {
		recv, err := client.ReadRecv()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, channelID, recv.ChannelID, cluster.DumpDiagnostics())
		prefix, ok := senders[recv.FromUID]
		require.True(t, ok, "unexpected group sender")
		require.Equal(t, fmt.Sprintf("e2e-sequence-group-%s-%06d", prefix, next[recv.FromUID]), recv.ClientMsgNo, "group sender input order")
		next[recv.FromUID]++
		require.Greater(t, recv.MessageSeq, previous,
			"group-channel receive sequence regressed from %d to %d\n%s", previous, recv.MessageSeq, cluster.DumpDiagnostics())
		require.NoError(t, client.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
		previous = recv.MessageSeq
	}
	return previous
}

func startLifecycleCluster(t *testing.T) *suite.StartedCluster {
	t.Helper()
	overrides := map[string]string{
		// Lifecycle clients intentionally exercise tokenless protocol traffic.
		"WK_GATEWAY_TOKEN_AUTH_ON":      "false",
		"WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
		"WK_CLUSTER_HASH_SLOT_COUNT":    "256",
		"WK_CLUSTER_SLOT_REPLICA_N":     "3",
		"WK_CLUSTER_CHANNEL_REPLICA_N":  "3",
		"WK_CLUSTER_MAX_CHANNELS":       "50000",
		"WK_GATEWAY_SEND_TIMEOUT":       "20s",
		"WK_BENCH_API_ENABLE":           "true",
		"WK_BENCH_API_TOKEN":            benchToken,
		"WK_BENCH_API_MAX_BATCH_SIZE":   "1200",
		"WK_METRICS_ENABLE":             "true",
		"WK_DEBUG_API_ENABLE":           "true",
	}
	options := []suite.Option{
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	}
	if artifactRoot := os.Getenv("WK_E2E_CHAT_LIFECYCLE_ARTIFACT_ROOT"); artifactRoot != "" {
		options = append(options, suite.WithWorkspaceRootDir(artifactRoot))
	}
	return suite.New(t).StartThreeNodeCluster(options...)
}

func lifecycleTarget(cluster *suite.StartedCluster) *target.Client {
	addrs := make([]string, 0, len(cluster.Nodes))
	for i := range cluster.Nodes {
		addrs = append(addrs, "http://"+cluster.Nodes[i].APIAddr())
	}
	return target.NewClient(target.Config{APIAddrs: addrs, Token: benchToken})
}

func connectAndFullSync(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	api *target.Client,
	nodeID uint64,
	uid string,
	probes ...*ackWireProbe,
) *suite.WKProtoClient {
	t.Helper()
	client, err := suite.NewWKProtoClientWithTimeout(operationTimeout)
	if len(probes) > 0 {
		client, err = suite.NewWKProtoClientWithDialer(operationTimeout, probes[0])
	}
	require.NoError(t, err)
	require.NoError(t, client.Connect(cluster.MustNode(nodeID).GatewayAddr(), uid, uid+"-device"), cluster.DumpDiagnostics())
	rows, err := api.ConversationSync(ctx, benchlifecycle.NewConversationSyncRequest(uid))
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.NoError(t, benchlifecycle.ValidateConversationSync(rows), cluster.DumpDiagnostics())
	return client
}

func connectAndFullSyncContaining(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	api *target.Client,
	nodeID uint64,
	uid string,
	clientMsgNo string,
) *suite.WKProtoClient {
	t.Helper()
	client, err := suite.NewWKProtoClientWithTimeout(operationTimeout)
	require.NoError(t, err)
	require.NoError(t, client.Connect(cluster.MustNode(nodeID).GatewayAddr(), uid, uid+"-device-reheat"), cluster.DumpDiagnostics())

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastRows []target.ConversationSyncConversation
	for {
		rows, syncErr := api.ConversationSync(ctx, benchlifecycle.NewConversationSyncRequest(uid))
		if syncErr == nil {
			lastRows = rows
			syncErr = benchlifecycle.ValidateConversationSync(rows)
		}
		if syncErr == nil && syncContains(rows, clientMsgNo) {
			return client
		}
		select {
		case <-ctx.Done():
			t.Fatalf("full login sync canceled: rows=%d err=%v\n%s", len(lastRows), ctx.Err(), cluster.DumpDiagnostics())
		case <-deadline.C:
			t.Fatalf("full login sync did not reconstruct prior message: rows=%d\n%s", len(lastRows), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func syncContains(rows []target.ConversationSyncConversation, clientMsgNo string) bool {
	for _, row := range rows {
		if row.LastMessage != nil && row.LastMessage.ClientMsgNo == clientMsgNo {
			return true
		}
	}
	return false
}

func sendPersonMessage(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender, recipient *suite.WKProtoClient,
	peerUID string,
	clientSeq uint64,
	clientMsgNo string,
) uint64 {
	t.Helper()
	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID: peerUID, ChannelType: frame.ChannelTypePerson,
		ClientSeq: clientSeq, ClientMsgNo: clientMsgNo, Payload: []byte("chat lifecycle evidence"),
	}), cluster.DumpDiagnostics())
	ack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode, cluster.DumpDiagnostics())
	recv, err := recipient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, ack.MessageID, recv.MessageID)
	require.Equal(t, ack.MessageSeq, recv.MessageSeq)
	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
	return ack.MessageSeq
}

func requireLoadedLeader(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	api *target.Client,
	req model.ChannelRuntimeProbeRequest,
	committedSeq uint64,
) []model.ChannelRuntimeProbeResult {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last []model.ChannelRuntimeProbeResult
	var lastErr error
	for {
		last, lastErr = api.ProbeChannelRuntimeAll(ctx, req)
		if lastErr == nil && loadedLeaderEvidenceValid(last, committedSeq) {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("runtime load probe canceled: %v; evidence=%s\n%s", ctx.Err(), summarizeProbe(last), cluster.DumpDiagnostics())
		case <-deadline.C:
			t.Fatalf("Leader runtime did not expose the acknowledged commit: err=%v evidence=%s\n%s", lastErr, summarizeProbe(last), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func loadedLeaderEvidenceValid(results []model.ChannelRuntimeProbeResult, committedSeq uint64) bool {
	if len(results) != 3 {
		return false
	}
	leaders := 0
	for _, result := range results {
		if result.Checked != 1 || len(result.Channels) != 1 {
			return false
		}
		row := result.Channels[0]
		switch row.Role {
		case "leader":
			if row.Status != "active" || row.HW < committedSeq || row.LEO < row.HW {
				return false
			}
			leaders++
		case "follower":
			if row.Status != "active" {
				return false
			}
		case "missing":
			// QuorumLog replication does not require a follower runtime.
			if row.Status != "missing" {
				return false
			}
		default:
			return false
		}
	}
	return leaders == 1
}

func missingEvidenceValid(results []model.ChannelRuntimeProbeResult) bool {
	if len(results) != 3 {
		return false
	}
	for _, result := range results {
		if result.Checked != 1 || result.LoadedLeader != 0 || result.LoadedFollower != 0 ||
			len(result.Channels) != 1 || result.Channels[0].Role != "missing" || result.Channels[0].Status != "missing" {
			return false
		}
	}
	return true
}

func requireMonotonicRuntimeEvidence(t *testing.T, before, after []model.ChannelRuntimeProbeResult) {
	t.Helper()
	if len(before) != 3 || len(after) != 3 {
		t.Fatalf("invalid lifecycle evidence cardinality: before=%d after=%d", len(before), len(after))
	}
	maxBefore := uint64(0)
	for _, result := range before {
		maxBefore = max(maxBefore, result.Channels[0].HW)
	}
	for _, result := range after {
		if result.Channels[0].Role != "leader" {
			continue
		}
		if result.Channels[0].HW <= maxBefore || result.Channels[0].LEO < result.Channels[0].HW {
			t.Fatalf("reheated runtime did not advance monotonically: before=%s after=%s", summarizeProbe(before), summarizeProbe(after))
		}
	}
}

func requireCreatedTotal(t *testing.T, ctx context.Context, cluster *suite.StartedCluster) uint64 {
	t.Helper()
	var total uint64
	for i := range cluster.Nodes {
		nodeClient := target.NewClient(target.Config{APIAddrs: []string{"http://" + cluster.Nodes[i].APIAddr()}, Token: benchToken})
		snapshot, err := nodeClient.Metrics(ctx)
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.NoError(t, snapshot.ValidateRequired(), cluster.DumpDiagnostics())
		for _, slot := range snapshot.MetaCreatedBySlot {
			total += slot.Created
		}
	}
	return total
}

func requireCreatedTotalEventually(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	want uint64,
) uint64 {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var got uint64
	var lastErr error
	for {
		got, lastErr = createdTotal(ctx, cluster)
		if lastErr == nil && got == want {
			return got
		}
		if lastErr == nil && got > want {
			t.Fatalf("authoritative metadata create total = %d, want %d", got, want)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metadata metric canceled: %v", ctx.Err())
		case <-deadline.C:
			t.Fatalf("authoritative metadata create total = %d, want %d: %v\n%s", got, want, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func createdTotal(ctx context.Context, cluster *suite.StartedCluster) (uint64, error) {
	var total uint64
	for i := range cluster.Nodes {
		nodeClient := target.NewClient(target.Config{APIAddrs: []string{"http://" + cluster.Nodes[i].APIAddr()}, Token: benchToken})
		snapshot, err := nodeClient.Metrics(ctx)
		if err != nil {
			return 0, err
		}
		if err := snapshot.ValidateRequired(); err != nil {
			return 0, err
		}
		for _, slot := range snapshot.MetaCreatedBySlot {
			total += slot.Created
		}
	}
	return total, nil
}

func summarizeProbe(results []model.ChannelRuntimeProbeResult) string {
	if len(results) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if len(result.Channels) != 1 {
			parts = append(parts, fmt.Sprintf("node=%d rows=%d", result.NodeID, len(result.Channels)))
			continue
		}
		row := result.Channels[0]
		parts = append(parts, fmt.Sprintf("node=%d role=%s status=%s leo=%d hw=%d", result.NodeID, row.Role, row.Status, row.LEO, row.HW))
	}
	return fmt.Sprintf("%v", parts)
}

// ackWireProbe observes ordered public WKProto bytes before future scheduling.
// It never delays or rewrites application traffic and retains at most 1024 ACKs.
type ackWireProbe struct{ acks chan frame.SendackPacket }

func newAckWireProbe() *ackWireProbe {
	return &ackWireProbe{acks: make(chan frame.SendackPacket, 1024)}
}
func (p *ackWireProbe) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &ackWireConn{Conn: c, probe: p, protocol: codec.New()}, nil
}

type ackWireConn struct {
	net.Conn
	probe    *ackWireProbe
	protocol codec.Protocol
	pending  []byte
}

func (c *ackWireConn) Read(dst []byte) (int, error) {
	n, err := c.Conn.Read(dst)
	if n == 0 {
		return n, err
	}
	if len(c.pending)+n > 1<<20 {
		return n, fmt.Errorf("wire probe frame buffer exceeded bound")
	}
	c.pending = append(c.pending, dst[:n]...)
	for len(c.pending) > 0 {
		packet, consumed, decodeErr := c.protocol.DecodeFrame(c.pending, frame.LatestVersion)
		if decodeErr != nil {
			return n, decodeErr
		}
		if packet == nil || consumed == 0 {
			break
		}
		if ack, ok := packet.(*frame.SendackPacket); ok {
			copy := frame.SendackPacket{ClientSeq: ack.ClientSeq, MessageSeq: ack.MessageSeq, ReasonCode: ack.ReasonCode}
			select {
			case c.probe.acks <- copy:
			default:
				return n, fmt.Errorf("wire probe ACK buffer exceeded bound")
			}
		}
		c.pending = c.pending[consumed:]
	}
	return n, err
}
