package chatlifecycle

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

// PreflightOutcome is the closed result class used to gate lifecycle traffic.
type PreflightOutcome string

const (
	PreflightPass                  PreflightOutcome = "pass"
	PreflightHarnessInvalid        PreflightOutcome = "harness_invalid"
	PreflightInfrastructureFailure PreflightOutcome = "infrastructure_failure"
)

// PreflightCode is a bounded, non-secret reason for a preflight outcome.
type PreflightCode string

const (
	PreflightCodeOK                PreflightCode = "ok"
	PreflightCodeTopology          PreflightCode = "topology"
	PreflightCodeTargetUnavailable PreflightCode = "target_unavailable"
	PreflightCodeTargetConfig      PreflightCode = "target_config"
	PreflightCodeCluster           PreflightCode = "cluster"
	PreflightCodeMetrics           PreflightCode = "metrics"
	PreflightCodeBenchCapability   PreflightCode = "bench_capability"
	PreflightCodeWorkerUnavailable PreflightCode = "worker_unavailable"
	PreflightCodeGateway           PreflightCode = "gateway"
	PreflightCodeUnauthorized      PreflightCode = "unauthorized"
	PreflightCodeDiskAmbiguous     PreflightCode = "disk_ambiguous"
	PreflightCodeDiskCapacity      PreflightCode = "disk_capacity"
	PreflightCodeDiskFree          PreflightCode = "disk_free"
)

// PreflightResult deliberately carries no raw error text or credential value.
type PreflightResult struct {
	Outcome PreflightOutcome `json:"outcome"`
	Code    PreflightCode    `json:"code"`
}

// Passed reports whether every prerequisite was observed successfully.
func (r PreflightResult) Passed() bool {
	return r.Outcome == PreflightPass && r.Code == PreflightCodeOK
}

// TrafficAllowed is the sole positive gate; all unknown and zero results deny traffic.
func (r PreflightResult) TrafficAllowed() bool { return r.Passed() }

// PreflightTarget is the black-box service/API surface required before traffic.
type PreflightTarget interface {
	Healthz(context.Context) error
	Readyz(context.Context) error
	DebugConfig(context.Context) (target.DebugConfig, error)
	DebugCluster(context.Context) (target.DebugCluster, error)
	CheckMetrics(context.Context) error
	ForceGC(context.Context) error
	Capabilities(context.Context) (model.BenchCapabilities, error)
}

// PreflightWorker is the authenticated worker liveness/capability boundary.
type PreflightWorker interface {
	Check(context.Context) error
}

// DiskReader selects one exact declared data filesystem.
type DiskReader interface {
	Filesystem(context.Context) (DataFilesystem, error)
}

type preflightTarget = PreflightTarget
type preflightWorker = PreflightWorker
type diskReader = DiskReader

// GatewayChecker verifies the separately declared TCP gateway pool.
type GatewayChecker interface {
	Check(context.Context, []string) error
}

// GatewayCheckerFunc adapts a function to GatewayChecker.
type GatewayCheckerFunc func(context.Context, []string) error

func (f GatewayCheckerFunc) Check(ctx context.Context, addrs []string) error { return f(ctx, addrs) }

// CoordinatedStop receives a narrow failure signal. It does not own assignment control.
type CoordinatedStop interface {
	Signal(context.Context, PreflightResult) error
}

// CoordinatedStopFunc adapts a function to CoordinatedStop.
type CoordinatedStopFunc func(context.Context, PreflightResult) error

func (f CoordinatedStopFunc) Signal(ctx context.Context, result PreflightResult) error {
	return f(ctx, result)
}

// PreflightOptions supplies credentials and replaceable I/O boundaries.
type PreflightOptions struct {
	BenchToken   string
	WorkerTokens map[string]string
	HTTPClient   *http.Client

	TargetFactory  func(int, EndpointDeclaration, string) PreflightTarget
	WorkerFactory  func(int, EndpointDeclaration, string) PreflightWorker
	DiskFactory    func(int, EndpointDeclaration) DiskReader
	GatewayChecker GatewayChecker
	SafeStop       CoordinatedStop
}

// Preflight proves the declared topology and observation surfaces before traffic.
type Preflight struct {
	options PreflightOptions
}

// NewPreflight constructs a preflight with bounded production adapters for omitted seams.
func NewPreflight(options PreflightOptions) *Preflight {
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if options.TargetFactory == nil {
		options.TargetFactory = func(_ int, endpoint EndpointDeclaration, token string) PreflightTarget {
			client := target.NewClient(target.Config{APIAddrs: []string{endpoint.Address}, Token: token, HTTPClient: options.HTTPClient})
			return targetPreflightClient{client: client}
		}
	}
	if options.WorkerFactory == nil {
		options.WorkerFactory = func(_ int, endpoint EndpointDeclaration, token string) PreflightWorker {
			client, err := NewWorkerClient(WorkerClientConfig{BaseURL: endpoint.Address, ControlToken: token, HTTPClient: options.HTTPClient})
			return workerPreflightClient{client: client, err: err}
		}
	}
	if options.DiskFactory == nil {
		options.DiskFactory = func(_ int, endpoint EndpointDeclaration) DiskReader {
			return newNodeExporterDiskReader(endpoint, options.HTTPClient)
		}
	}
	if options.GatewayChecker == nil {
		options.GatewayChecker = tcpGatewayChecker{dialer: &net.Dialer{Timeout: 5 * time.Second}}
	}
	return &Preflight{options: options}
}

// Check performs no network I/O until static topology validation succeeds.
func (p *Preflight) Check(ctx context.Context, cfg Config) PreflightResult {
	if p == nil || cfg.Validate() != nil || strings.TrimSpace(p.options.BenchToken) == "" {
		return invalid(PreflightCodeTopology)
	}

	snapshots := make([]target.DebugCluster, 0, len(cfg.Observation.ServiceNodes))
	nodeIDs := make(map[uint64]struct{}, len(cfg.Observation.ServiceNodes))
	for index, endpoint := range cfg.Observation.ServiceNodes {
		client := p.options.TargetFactory(index, endpoint, p.options.BenchToken)
		if client == nil {
			return invalid(PreflightCodeTargetUnavailable)
		}
		if err := client.Healthz(ctx); err != nil {
			return classifyInvalid(err, PreflightCodeTargetUnavailable)
		}
		if err := client.Readyz(ctx); err != nil {
			return classifyInvalid(err, PreflightCodeTargetUnavailable)
		}
		observedConfig, err := client.DebugConfig(ctx)
		if err != nil {
			return classifyInvalid(err, PreflightCodeTargetConfig)
		}
		if !matchesPreflightConfig(observedConfig, cfg) {
			return invalid(PreflightCodeTargetConfig)
		}
		if _, duplicate := nodeIDs[observedConfig.NodeID]; observedConfig.NodeID == 0 || duplicate {
			return invalid(PreflightCodeTargetConfig)
		}
		nodeIDs[observedConfig.NodeID] = struct{}{}
		if err := client.ForceGC(ctx); err != nil {
			return classifyInvalid(err, PreflightCodeMetrics)
		}
		if err := client.CheckMetrics(ctx); err != nil {
			return classifyInvalid(err, PreflightCodeMetrics)
		}
		snapshot, err := client.DebugCluster(ctx)
		if err != nil {
			return classifyInvalid(err, PreflightCodeCluster)
		}
		if snapshot.NodeID != observedConfig.NodeID {
			return invalid(PreflightCodeCluster)
		}
		snapshots = append(snapshots, snapshot)
	}
	cluster, err := mergeClusterObservations(snapshots, cfg)
	if err != nil {
		return invalid(PreflightCodeCluster)
	}
	hotHealthy, validHotSet := hotSlotProgressHealthy(cluster, nil)
	if !validHotSet || !hotHealthy {
		return invalid(PreflightCodeCluster)
	}

	for index, address := range cfg.Observation.APIAddrs {
		endpoint := EndpointDeclaration{Name: "api", Address: address}
		client := p.options.TargetFactory(index, endpoint, p.options.BenchToken)
		if client == nil {
			return invalid(PreflightCodeBenchCapability)
		}
		capabilities, err := client.Capabilities(ctx)
		if err != nil {
			return classifyInvalid(err, PreflightCodeBenchCapability)
		}
		if !supportsLifecyclePreflight(capabilities) {
			return invalid(PreflightCodeBenchCapability)
		}
	}

	for index, endpoint := range cfg.Observation.Workers {
		token := strings.TrimSpace(p.options.WorkerTokens[endpoint.Name])
		if token == "" {
			return invalid(PreflightCodeUnauthorized)
		}
		worker := p.options.WorkerFactory(index, endpoint, token)
		if worker == nil {
			return invalid(PreflightCodeWorkerUnavailable)
		}
		if err := worker.Check(ctx); err != nil {
			return classifyInvalid(err, PreflightCodeWorkerUnavailable)
		}
	}
	if err := p.options.GatewayChecker.Check(ctx, cfg.Observation.GatewayTCPAddrs); err != nil {
		return invalid(PreflightCodeGateway)
	}

	for index, endpoint := range cfg.Observation.HostMetrics {
		reader := p.options.DiskFactory(index, endpoint)
		if reader == nil {
			return invalid(PreflightCodeDiskAmbiguous)
		}
		filesystem, err := reader.Filesystem(ctx)
		if err != nil {
			return invalid(PreflightCodeDiskAmbiguous)
		}
		if filesystem.SizeBytes < cfg.Thresholds.MinimumDataFilesystemBytes || filesystem.SizeBytes <= 0 || filesystem.AvailableBytes < 0 || filesystem.AvailableBytes > filesystem.SizeBytes {
			return infrastructure(PreflightCodeDiskCapacity)
		}
		if diskFreeBelow(filesystem, cfg.Thresholds.DiskSafeStopFreePercent) {
			result := infrastructure(PreflightCodeDiskFree)
			if p.options.SafeStop != nil {
				_ = p.options.SafeStop.Signal(ctx, result)
			}
			return result
		}
	}
	return PreflightResult{Outcome: PreflightPass, Code: PreflightCodeOK}
}

func matchesPreflightConfig(observed target.DebugConfig, cfg Config) bool {
	return observed.InitialSlotCount == uint32(cfg.Workload.Topology.LogicalSlotGroups) &&
		observed.HashSlotCount == uint16(cfg.Workload.Topology.HashSlots) &&
		observed.SlotReplicaCount == cfg.Workload.Topology.SlotReplicas &&
		observed.ChannelReplicaCount == cfg.Workload.Topology.ChannelReplicas &&
		observed.MaxChannels == cfg.Workload.MaxChannelsPerNode
}

func supportsLifecyclePreflight(capabilities model.BenchCapabilities) bool {
	supports := capabilities.Supports
	hasPerson, hasGroup := false, false
	for _, channelType := range supports.ChannelTypes {
		switch channelType {
		case "person":
			hasPerson = true
		case "group":
			hasGroup = true
		}
	}
	return capabilities.Enabled && capabilities.Version == "bench/v1" &&
		supports.UsersTokensBatch && supports.ChannelsBatch && supports.ChannelSubscribersBatch &&
		supports.Snapshot && supports.PresenceSnapshot && supports.ChannelRuntimeSnapshot &&
		supports.ChannelRuntimeProbe && hasPerson && hasGroup
}

func diskFreeBelow(filesystem DataFilesystem, percent int) bool {
	whole := filesystem.SizeBytes / 100
	remainder := filesystem.SizeBytes % 100
	minimum := whole*int64(percent) + (remainder*int64(percent)+99)/100
	return filesystem.AvailableBytes < minimum
}

func classifyInvalid(err error, fallback PreflightCode) PreflightResult {
	if isUnauthorizedObservation(err) {
		return invalid(PreflightCodeUnauthorized)
	}
	return invalid(fallback)
}

func isUnauthorizedObservation(err error) bool {
	var apiError *WorkerAPIError
	if errors.As(err, &apiError) && (apiError.Status == http.StatusUnauthorized || apiError.Status == http.StatusForbidden || apiError.Code == WorkerErrorUnauthorized) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "status 401") || strings.Contains(message, "status 403")
}

func invalid(code PreflightCode) PreflightResult {
	return PreflightResult{Outcome: PreflightHarnessInvalid, Code: code}
}

func infrastructure(code PreflightCode) PreflightResult {
	return PreflightResult{Outcome: PreflightInfrastructureFailure, Code: code}
}

type targetPreflightClient struct{ client *target.Client }

func (c targetPreflightClient) Healthz(ctx context.Context) error { return c.client.Healthz(ctx) }
func (c targetPreflightClient) Readyz(ctx context.Context) error  { return c.client.Readyz(ctx) }
func (c targetPreflightClient) DebugConfig(ctx context.Context) (target.DebugConfig, error) {
	return c.client.DebugConfig(ctx)
}
func (c targetPreflightClient) DebugCluster(ctx context.Context) (target.DebugCluster, error) {
	return c.client.DebugCluster(ctx)
}
func (c targetPreflightClient) CheckMetrics(ctx context.Context) error {
	snapshot, err := c.client.Metrics(ctx)
	if err != nil {
		return err
	}
	return snapshot.ValidateRequired()
}
func (c targetPreflightClient) ForceGC(ctx context.Context) error { return c.client.ForceGC(ctx) }
func (c targetPreflightClient) Capabilities(ctx context.Context) (model.BenchCapabilities, error) {
	return c.client.Capabilities(ctx)
}

type workerPreflightClient struct {
	client *WorkerClient
	err    error
}

func (c workerPreflightClient) Check(ctx context.Context) error {
	if c.err != nil {
		return c.err
	}
	health, err := c.client.Health(ctx)
	if err != nil {
		return err
	}
	if !health.OK {
		return ErrWorkerResponse
	}
	info, err := c.client.Info(ctx)
	if err != nil {
		return err
	}
	if info.ProtocolVersion != workerProtocolVersion || info.MaxRequestBytes <= 0 || info.MaxRequestBytes > workerMaxRequestBytes || info.MaxResponseBytes <= 0 || info.MaxResponseBytes > workerMaxResponseBytes {
		return ErrWorkerResponse
	}
	return nil
}

type tcpGatewayChecker struct{ dialer *net.Dialer }

func (c tcpGatewayChecker) Check(ctx context.Context, addresses []string) error {
	for _, address := range addresses {
		connection, err := c.dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		if err := connection.Close(); err != nil {
			return err
		}
	}
	return nil
}
