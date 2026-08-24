package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
	benchreport "github.com/WuKongIM/WuKongIM/internal/bench/report"
	benchmodel "github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"gopkg.in/yaml.v3"
)

// localSingleNodeReviewedExecution is the small, derived projection of the
// coordinator-emitted effective scenario, deterministic plan, and run report.
// The raw artifacts remain closure inputs; callers cannot independently assert
// any value in this projection.
type localSingleNodeReviewedExecution struct {
	RunID                     string
	ReportStatus              benchreport.Status
	ReportExitCode            int
	ReportStabilityVerdict    benchreport.StabilityVerdict
	OfferedSendQPS            int
	RequiredActiveConnections int
	GroupMembers              int
	WarmupSeconds             int
	MeasuredSeconds           int
	DrainBudgetSeconds        int
	Target                    localbaseline.ReviewedTargetEvidence
	BaselineInvocationID      string
}

func parseLocalSingleNodeReviewedExecution(scenarioData, planData, reportData []byte) (localSingleNodeReviewedExecution, error) {
	var trailing any
	var report benchreport.Report
	jsonDecoder := json.NewDecoder(bytes.NewReader(reportData))
	jsonDecoder.DisallowUnknownFields()
	if err := jsonDecoder.Decode(&report); err != nil {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("decode report: %w", err)
	}
	if err := jsonDecoder.Decode(&trailing); err != io.EOF {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("decode report: trailing JSON")
	}
	canonicalScenario, err := yaml.Marshal(report.Scenario)
	if err != nil {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("encode canonical scenario: %w", err)
	}
	canonicalPlan, err := json.MarshalIndent(report.Plan, "", "  ")
	if err != nil {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("encode canonical plan: %w", err)
	}
	canonicalPlan = append(canonicalPlan, '\n')
	if !bytes.Equal(scenarioData, canonicalScenario) || !bytes.Equal(planData, canonicalPlan) ||
		report.RunID != report.Scenario.Run.ID || report.Plan.RunID != report.RunID {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("scenario or plan does not match canonical run report")
	}
	target, err := validateLocalSingleNodeReviewedReportShape(report)
	if err != nil {
		return localSingleNodeReviewedExecution{}, err
	}
	reviewed, err := validateLocalSingleNodeReviewedExecution(report.Scenario, report.Plan)
	if err != nil {
		return localSingleNodeReviewedExecution{}, err
	}
	reviewed.RunID = report.RunID
	reviewed.ReportStatus = report.Status
	reviewed.ReportExitCode = report.ExitCode
	reviewed.ReportStabilityVerdict = report.StabilityVerdict
	reviewed.Target = target
	invocationID, ok := reviewedLocalSingleNodeInvocationID(report.RunID, reviewed.OfferedSendQPS)
	if !ok {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("canonical run report invocation identity is invalid")
	}
	reviewed.BaselineInvocationID = invocationID
	return reviewed, nil
}

func reviewedLocalSingleNodeInvocationID(runID string, offeredSendQPS int) (string, bool) {
	const prefix = "single-node-"
	suffix := fmt.Sprintf("-fixed-1000ch-%06d-qps", offeredSendQPS)
	if !strings.HasPrefix(runID, prefix) || !strings.HasSuffix(runID, suffix) {
		return "", false
	}
	invocationID := strings.TrimSuffix(strings.TrimPrefix(runID, prefix), suffix)
	if len(invocationID) != 32 {
		return "", false
	}
	for _, character := range invocationID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", false
		}
	}
	return invocationID, true
}

func validateLocalSingleNodeReviewedExecution(scenario benchmodel.Scenario, plan benchmodel.Plan) (localSingleNodeReviewedExecution, error) {
	if scenario.Version != "wkbench/v1" || strings.TrimSpace(scenario.Run.ID) == "" || plan.RunID != scenario.Run.ID {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("run identity is invalid")
	}
	warmupSeconds, warmupOK := exactPositiveDurationSeconds(scenario.Run.Warmup)
	measuredSeconds, measuredOK := exactPositiveDurationSeconds(scenario.Run.Duration)
	drainSeconds, drainOK := exactPositiveDurationSeconds(scenario.Run.Cooldown)
	if !warmupOK || !measuredOK || !drainOK || !scenario.Run.ExternalTerminalCut || scenario.Run.RandomSeed != 0 || !scenario.Run.FailFast {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("run windows or terminal contract are invalid")
	}
	if scenario.Objectives.Standard || !sameFiniteFloat(scenario.Objectives.ToleranceRatio, 0.1) ||
		scenario.Online.TotalUsers <= 0 || scenario.Online.GatewayBalance != "round_robin" ||
		!scenario.Online.Heartbeat.Enabled || scenario.Online.Reconnect.Enabled || scenario.Online.Churn.Enabled ||
		scenario.Cleanup.Enabled || scenario.Limits.FailOnSoft ||
		scenario.Limits.Hard.MaxWorkerFailed != 0 || scenario.Limits.Hard.MaxConnectErrorRate != 0 ||
		scenario.Limits.Hard.MaxSendackErrorRate != 0 || scenario.Limits.Hard.MaxRecvVerifyErrorRate != 0 {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("online or failure policy is outside the reviewed contract")
	}
	if len(scenario.Channels.Profiles) != 1 || len(scenario.Messages.Traffic) != 1 {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("reviewed execution requires exactly one group profile and one traffic stream")
	}
	profile := scenario.Channels.Profiles[0]
	traffic := scenario.Messages.Traffic[0]
	if profile.Name == "" || profile.ChannelType != benchmodel.ChannelTypeGroup || profile.Count != 1000 ||
		profile.Members.Count <= 1 || profile.Members.Overlap != "allowed" || !sameFiniteFloat(profile.Online.MemberRatio, 1) ||
		profile.Shard.Mode != "hash" || profile.Shard.HashSlotSpread || profile.Shard.HashSlotCount != 0 ||
		profile.Prepare.SubscribersBatchSize <= 0 ||
		traffic.Name == "" || traffic.ChannelRef != profile.Name || traffic.Concurrency != 2800 ||
		traffic.AckTimeout != 15*time.Second || !traffic.Retry.Enabled || traffic.SenderPick != "round_robin" ||
		!traffic.RecvAck || strings.TrimSpace(traffic.Verify.Recv.Mode) != "none" ||
		scenario.Messages.Payload.SizeBytes != 128 || scenario.Messages.Payload.Mode != "deterministic" ||
		strings.TrimSpace(scenario.Identity.ClientMsgPrefix) == "" || scenario.Identity.Token.Mode != "bench_api" {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("group traffic shape is outside the reviewed contract")
	}
	offered := scenario.Objectives.IngressQPS.PerSecond
	if offered <= 0 || offered > float64(math.MaxInt) || math.Trunc(offered) != offered ||
		!sameFiniteFloat(traffic.RatePerChannel.PerSecond*float64(profile.Count), offered) ||
		!sameFiniteFloat(scenario.Objectives.OnlineFanoutQPS.PerSecond, offered*float64(profile.Members.Count-1)) {
		return localSingleNodeReviewedExecution{}, fmt.Errorf("offered and fanout rates are inconsistent")
	}
	if err := validateLocalSingleNodeReviewedPlan(plan, scenario, profile, traffic); err != nil {
		return localSingleNodeReviewedExecution{}, err
	}
	return localSingleNodeReviewedExecution{
		OfferedSendQPS:            int(offered),
		RequiredActiveConnections: scenario.Online.TotalUsers,
		GroupMembers:              profile.Members.Count,
		WarmupSeconds:             warmupSeconds,
		MeasuredSeconds:           measuredSeconds,
		DrainBudgetSeconds:        drainSeconds,
	}, nil
}

func validateLocalSingleNodeReviewedReportShape(report benchreport.Report) (localbaseline.ReviewedTargetEvidence, error) {
	if err := benchreport.ValidateRetainedCredentials(report); err != nil {
		return localbaseline.ReviewedTargetEvidence{}, fmt.Errorf("canonical run report credential boundary: %w", err)
	}
	target := report.Target
	if target.Name != "local-single-node-cluster" ||
		len(target.API.Addrs) != 1 || strings.TrimSpace(target.API.Addrs[0]) == "" ||
		len(target.Gateway.TCP.Addrs) != 1 || strings.TrimSpace(target.Gateway.TCP.Addrs[0]) == "" ||
		!target.BenchAPI.Enabled || len(target.BenchAPI.Addrs) != 1 ||
		strings.TrimSpace(target.BenchAPI.Addrs[0]) == "" || target.BenchAPI.Addrs[0] != target.API.Addrs[0] ||
		strings.TrimSpace(target.BenchAPI.Token) != "" ||
		!target.Metrics.Enabled || len(target.Metrics.Addrs) != 1 || target.Metrics.Addrs[0] != target.API.Addrs[0] {
		return localbaseline.ReviewedTargetEvidence{}, fmt.Errorf("canonical run report target is outside the reviewed single-node cluster shape")
	}
	if len(report.Workers.Workers) != 1 || len(report.Plan.WorkerOrder) != 1 {
		return localbaseline.ReviewedTargetEvidence{}, fmt.Errorf("canonical run report requires exactly one worker")
	}
	worker := report.Workers.Workers[0]
	if worker.ID != report.Plan.WorkerOrder[0] || strings.TrimSpace(worker.Addr) == "" ||
		!sameFiniteFloat(worker.Weight, 1) || !worker.InsecureControl || strings.TrimSpace(worker.ControlToken) != "" ||
		worker.Client != nil || worker.TCPSource != nil || len(worker.Tags) != 0 {
		return localbaseline.ReviewedTargetEvidence{}, fmt.Errorf("canonical run report worker does not match the reviewed worker assignment")
	}
	apiAddress, apiOK := canonicalReviewedLoopbackHTTPAddress(target.API.Addrs[0])
	gatewayAddress, gatewayOK := canonicalReviewedLoopbackTCPAddress(target.Gateway.TCP.Addrs[0])
	workerAddress, workerOK := canonicalReviewedLoopbackHTTPAddress(worker.Addr)
	if !apiOK || !gatewayOK || !workerOK {
		return localbaseline.ReviewedTargetEvidence{}, fmt.Errorf("canonical run report endpoints are not exact loopback listeners")
	}
	return localbaseline.ReviewedTargetEvidence{
		APIAddress: apiAddress, GatewayAddress: gatewayAddress,
		MetricsAddress: apiAddress, WorkerAddress: workerAddress,
	}, nil
}

func canonicalReviewedLoopbackHTTPAddress(value string) (string, bool) {
	if value != strings.TrimSpace(value) {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	hostPort, ok := canonicalReviewedLoopbackTCPAddress(parsed.Host)
	if !ok {
		return "", false
	}
	canonical := "http://" + hostPort
	return canonical, value == canonical
}

func canonicalReviewedLoopbackTCPAddress(value string) (string, bool) {
	if value != strings.TrimSpace(value) {
		return "", false
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", false
	}
	ip := net.ParseIP(host)
	portNumber, portErr := strconv.ParseUint(port, 10, 16)
	if ip == nil || !ip.IsLoopback() || portErr != nil || portNumber == 0 {
		return "", false
	}
	canonical := net.JoinHostPort(ip.String(), strconv.FormatUint(portNumber, 10))
	return canonical, value == canonical
}

func validateLocalSingleNodeReviewedPlan(plan benchmodel.Plan, scenario benchmodel.Scenario, profile benchmodel.ChannelProfile, traffic benchmodel.TrafficConfig) error {
	if len(plan.Workers) != 1 || len(plan.WorkerOrder) != 1 || len(plan.ProfileOrder) != 1 || plan.ProfileOrder[0] != profile.Name ||
		plan.IdentityPool.Start != 0 || plan.IdentityPool.Len() != scenario.Online.TotalUsers ||
		plan.OnlineIdentityPool != plan.IdentityPool {
		return fmt.Errorf("deterministic plan topology is invalid")
	}
	workerID := plan.WorkerOrder[0]
	worker, ok := plan.Workers[workerID]
	if !ok || worker.WorkerID != workerID || worker.IdentityRange != plan.OnlineIdentityPool ||
		len(worker.OnlineIdentityIndexes) != 0 || len(worker.Profiles) != 1 {
		return fmt.Errorf("deterministic worker assignment is invalid")
	}
	shard, ok := worker.Profiles[profile.Name]
	if !ok || shard.Name != profile.Name || shard.ChannelType != benchmodel.ChannelTypeGroup ||
		shard.ChannelRange.Start != 0 || shard.ChannelRange.Len() != profile.Count ||
		shard.MemberRange != plan.IdentityPool || shard.MemberReusePolicy != "allowed" ||
		!sameFiniteFloat(shard.GlobalRate.PerSecond, traffic.RatePerChannel.PerSecond) ||
		!sameFiniteFloat(shard.LocalRate.PerSecond, traffic.RatePerChannel.PerSecond) ||
		shard.TrafficPartitionCount != 0 || len(shard.OwnedTrafficPartitions) != 0 {
		return fmt.Errorf("deterministic group shard is invalid")
	}
	owners, ok := plan.ChannelOwners[profile.Name]
	if !ok || len(plan.ChannelOwners) != 1 || len(owners) != profile.Count {
		return fmt.Errorf("deterministic channel ownership is incomplete")
	}
	for channel := 0; channel < profile.Count; channel++ {
		if owners[channel] != workerID {
			return fmt.Errorf("deterministic channel ownership is invalid")
		}
	}
	return nil
}

func exactPositiveDurationSeconds(value time.Duration) (int, bool) {
	if value <= 0 || value%time.Second != 0 {
		return 0, false
	}
	seconds := value / time.Second
	if seconds > time.Duration(math.MaxInt) {
		return 0, false
	}
	return int(seconds), true
}

func sameFiniteFloat(left, right float64) bool {
	if math.IsNaN(left) || math.IsNaN(right) || math.IsInf(left, 0) || math.IsInf(right, 0) {
		return false
	}
	return math.Abs(left-right) <= 1e-9*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}
