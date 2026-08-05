package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

var errCapacityDatasetProbe = errors.New("chat lifecycle capacity dataset probe failed")

// CapacityDatasetProbeTarget exposes the two protected node-local identity
// documents consumed by the production capacity admission probe.
type CapacityDatasetProbeTarget interface {
	DebugConfig(context.Context) (target.DebugConfig, error)
	DebugGoroutineSummary(context.Context) (target.DebugGoroutineSummary, error)
}

// CapacityDatasetProbeClock stamps evidence in coordinator time.
type CapacityDatasetProbeClock interface {
	Now() time.Time
}

// CapacityDatasetProbeOptions supplies credentials and replaceable I/O seams.
type CapacityDatasetProbeOptions struct {
	BenchToken    string
	HTTPClient    *http.Client
	Clock         CapacityDatasetProbeClock
	TargetFactory func(int, EndpointDeclaration, string) CapacityDatasetProbeTarget
}

// CapacityDatasetProbe reads the current process generation directly from
// every declared service node and reduces it to one identity-free digest.
type CapacityDatasetProbe struct {
	options CapacityDatasetProbeOptions
}

var _ CoordinatorCapacityDatasetProbe = (*CapacityDatasetProbe)(nil)

// NewCapacityDatasetProbe constructs the production all-node probe.
func NewCapacityDatasetProbe(options CapacityDatasetProbeOptions) *CapacityDatasetProbe {
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if options.Clock == nil {
		options.Clock = realObserverClock{}
	}
	if options.TargetFactory == nil {
		options.TargetFactory = func(_ int, endpoint EndpointDeclaration, token string) CapacityDatasetProbeTarget {
			return target.NewClient(target.Config{
				APIAddrs: []string{endpoint.Address}, Token: token, HTTPClient: options.HTTPClient,
			})
		}
	}
	return &CapacityDatasetProbe{options: options}
}

type capacityDatasetNodeResult struct {
	index      int
	config     target.DebugConfig
	process    target.DebugGoroutineSummary
	observedAt time.Time
	err        error
}

type capacityDatasetDigestNode struct {
	NodeID           uint64 `json:"node_id"`
	NodeDataDir      string `json:"node_data_dir"`
	BootID           string `json:"boot_id"`
	ProcessStartedAt string `json:"process_started_at"`
}

// ProbeCapacityDataset implements CoordinatorCapacityDatasetProbe.
func (p *CapacityDatasetProbe) ProbeCapacityDataset(ctx context.Context, cfg Config) (CapacityLiveDatasetEvidence, error) {
	if cfg.Mode != ModeCapacity {
		return CapacityLiveDatasetEvidence{}, errCapacityDatasetProbe
	}
	return p.probeDataset(ctx, cfg)
}

// ProbeDatasetDigest reads the same three-node process identity for a Soak or
// Capacity run without treating process age as admission evidence.
func (p *CapacityDatasetProbe) ProbeDatasetDigest(ctx context.Context, cfg Config) (string, error) {
	evidence, err := p.probeDataset(ctx, cfg)
	if err != nil {
		return "", err
	}
	digest := evidence.Nodes[0].DatasetDigest
	if !validReportHash(digest) {
		return "", errCapacityDatasetProbe
	}
	for _, node := range evidence.Nodes[1:] {
		if node.DatasetDigest != digest {
			return "", errCapacityDatasetProbe
		}
	}
	return digest, nil
}

func (p *CapacityDatasetProbe) probeDataset(ctx context.Context, cfg Config) (CapacityLiveDatasetEvidence, error) {
	if p == nil || ctx == nil || cfg.Validate() != nil ||
		strings.TrimSpace(p.options.BenchToken) == "" || len(cfg.Observation.ServiceNodes) != coordinatorWorkerCount {
		return CapacityLiveDatasetEvidence{}, errCapacityDatasetProbe
	}
	results := make(chan capacityDatasetNodeResult, coordinatorWorkerCount)
	for index, endpoint := range cfg.Observation.ServiceNodes {
		go func(index int, endpoint EndpointDeclaration) {
			client := p.options.TargetFactory(index, endpoint, p.options.BenchToken)
			if client == nil {
				results <- capacityDatasetNodeResult{index: index, err: errCapacityDatasetProbe}
				return
			}
			config, err := client.DebugConfig(ctx)
			if err != nil {
				results <- capacityDatasetNodeResult{index: index, err: err}
				return
			}
			process, err := client.DebugGoroutineSummary(ctx)
			results <- capacityDatasetNodeResult{
				index: index, config: config, process: process, err: err,
			}
		}(index, endpoint)
	}

	var observed [coordinatorWorkerCount]capacityDatasetNodeResult
	for range coordinatorWorkerCount {
		result := <-results
		if result.err == nil {
			result.observedAt = p.options.Clock.Now()
		}
		observed[result.index] = result
	}
	for _, result := range observed {
		if result.err != nil {
			if ctx.Err() != nil && errors.Is(result.err, ctx.Err()) {
				return CapacityLiveDatasetEvidence{}, ctx.Err()
			}
			return CapacityLiveDatasetEvidence{}, errCapacityDatasetProbe
		}
	}

	digestNodes := make([]capacityDatasetDigestNode, coordinatorWorkerCount)
	seen := make(map[uint64]struct{}, coordinatorWorkerCount)
	for index, result := range observed {
		nodeID := result.config.NodeID
		dataDir := strings.TrimSpace(result.config.NodeDataDir)
		bootID := strings.TrimSpace(result.process.BootID)
		if nodeID == 0 || dataDir == "" || bootID == "" || result.observedAt.IsZero() ||
			result.process.GeneratedAt.IsZero() || result.process.ProcessStartedAt.IsZero() ||
			result.process.GeneratedAt.Before(result.process.ProcessStartedAt) {
			return CapacityLiveDatasetEvidence{}, errCapacityDatasetProbe
		}
		if _, duplicate := seen[nodeID]; duplicate {
			return CapacityLiveDatasetEvidence{}, errCapacityDatasetProbe
		}
		seen[nodeID] = struct{}{}
		digestNodes[index] = capacityDatasetDigestNode{
			NodeID: nodeID, NodeDataDir: dataDir, BootID: bootID,
			ProcessStartedAt: result.process.ProcessStartedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	sort.Slice(digestNodes, func(i, j int) bool { return digestNodes[i].NodeID < digestNodes[j].NodeID })
	encoded, err := json.Marshal(struct {
		Version string                      `json:"version"`
		Nodes   []capacityDatasetDigestNode `json:"nodes"`
	}{Version: "wukongim/chat-lifecycle-dataset/v1", Nodes: digestNodes})
	if err != nil {
		return CapacityLiveDatasetEvidence{}, errCapacityDatasetProbe
	}
	digest := hashReportValue(string(encoded))
	var evidence CapacityLiveDatasetEvidence
	for index, result := range observed {
		state := CapacityDatasetUnavailable
		if result.process.GeneratedAt.Sub(result.process.ProcessStartedAt) >= cfg.Capacity.AgedCheckpoint.Duration {
			state = CapacityDatasetLiveAged
		}
		evidence.Nodes[index] = CapacityLiveDatasetNodeEvidence{
			NodeID: result.config.NodeID, DatasetDigest: digest,
			ObservedAt: result.observedAt, State: state,
		}
	}
	return evidence, nil
}
