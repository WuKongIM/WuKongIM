package coordinator

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const workerInfoTimeout = 10 * time.Second

// GatewayChecker verifies gateway reachability before a run starts.
type GatewayChecker interface {
	Check(ctx context.Context, addrs []string) error
}

// PreflightConfig wires black-box preflight dependencies.
type PreflightConfig struct {
	// HTTPClient overrides HTTP calls to workers and targets for tests.
	HTTPClient *http.Client
	// GatewayChecker verifies target gateway addresses; nil uses a no-op checker.
	GatewayChecker GatewayChecker
}

// Preflight validates target and worker reachability before orchestration.
type Preflight struct {
	http           *http.Client
	gatewayChecker GatewayChecker
}

// NewPreflight builds a coordinator preflight checker.
func NewPreflight(cfg PreflightConfig) *Preflight {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: workerInfoTimeout}
	}
	checker := cfg.GatewayChecker
	if checker == nil {
		checker = noopGatewayChecker{}
	}
	return &Preflight{http: hc, gatewayChecker: checker}
}

// Check runs black-box target API, capability, worker, and gateway preflight checks.
func (p *Preflight) Check(ctx context.Context, tgt model.Target, workers model.WorkerSet) error {
	if !tgt.BenchAPI.Enabled {
		return fmt.Errorf("target.bench_api.enabled must be true")
	}
	apiAddrs := tgt.BenchAPI.Addrs
	if len(apiAddrs) == 0 {
		apiAddrs = tgt.API.Addrs
	}
	client := target.NewClient(target.Config{APIAddrs: apiAddrs, Token: tgt.BenchAPI.Token, HTTPClient: p.http})
	if err := client.Healthz(ctx); err != nil {
		return fmt.Errorf("target /healthz preflight failed: %w", err)
	}
	if err := client.Readyz(ctx); err != nil {
		return fmt.Errorf("target /readyz preflight failed: %w", err)
	}
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return fmt.Errorf("target capabilities preflight failed: %w", err)
	}
	if err := validateCapabilities(caps); err != nil {
		return err
	}
	for _, worker := range workers.Workers {
		if err := p.checkWorker(ctx, worker); err != nil {
			return err
		}
	}
	if err := p.gatewayChecker.Check(ctx, tgt.Gateway.TCP.Addrs); err != nil {
		return fmt.Errorf("gateway tcp preflight failed: %w", err)
	}
	return nil
}

func validateCapabilities(caps model.BenchCapabilities) error {
	var missing []string
	if caps.Version != "bench/v1" {
		missing = append(missing, fmt.Sprintf("version bench/v1 (got %q)", caps.Version))
	}
	if !caps.Enabled {
		missing = append(missing, "enabled")
	}
	if !caps.Supports.UsersTokensBatch {
		missing = append(missing, "users_tokens_batch")
	}
	if !caps.Supports.ChannelsBatch {
		missing = append(missing, "channels_batch")
	}
	if !caps.Supports.ChannelSubscribersBatch {
		missing = append(missing, "channel_subscribers_batch")
	}
	if !caps.Supports.Snapshot {
		missing = append(missing, "snapshot")
	}
	if !contains(caps.Supports.ChannelTypes, model.ChannelTypeGroup) {
		missing = append(missing, "channel_types group")
	}
	if len(missing) > 0 {
		return fmt.Errorf("target bench api capabilities missing required support: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (p *Preflight) checkWorker(ctx context.Context, worker model.Worker) error {
	addr := strings.TrimSpace(worker.Addr)
	if addr == "" {
		return fmt.Errorf("worker %s addr is required", strings.TrimSpace(worker.ID))
	}
	url := strings.TrimRight(addr, "/") + "/v1/info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("worker %s info request: %w", workerName(worker), err)
	}
	if worker.InsecureControl {
		// Explicit insecure workers must be probed without auth to mirror their control mode.
	} else if worker.ControlToken != "" {
		req.Header.Set("Authorization", "Bearer "+worker.ControlToken)
	} else {
		return fmt.Errorf("worker %s control_token is required unless insecure_control=true", workerName(worker))
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("worker %s /v1/info preflight failed: %w", workerName(worker), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		snippet := strings.TrimSpace(string(body))
		if snippet == "" {
			return fmt.Errorf("worker %s /v1/info returned status %d", workerName(worker), resp.StatusCode)
		}
		return fmt.Errorf("worker %s /v1/info returned status %d: %s", workerName(worker), resp.StatusCode, snippet)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func workerName(worker model.Worker) string {
	if id := strings.TrimSpace(worker.ID); id != "" {
		return id
	}
	return strings.TrimSpace(worker.Addr)
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

type noopGatewayChecker struct{}

func (noopGatewayChecker) Check(context.Context, []string) error { return nil }
