// Package fake implements a deterministic Cloud Provider Adapter for local
// lifecycle, cleanup, and failure-state verification.
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

const (
	// ProviderName is the stable identifier for the local fake adapter.
	ProviderName    = "fake"
	defaultCurrency = "CNY"
	stateSchemaV1   = "wukongim.cloud_sim.fake_inventory/v1"
	maxStateBytes   = 4 << 20
)

var (
	// ErrInjectedFailure reports an intentional fake-provider failure.
	ErrInjectedFailure = errors.New("internal/infra/cloudsim/fake: injected failure")
	// ErrInvalidTags reports a resource creation request without mandatory tags.
	ErrInvalidTags = errors.New("internal/infra/cloudsim/fake: invalid mandatory tags")
	// ErrStateStore reports an invalid or unavailable persistent fake inventory.
	ErrStateStore = errors.New("internal/infra/cloudsim/fake: state store")
)

// FailurePlan injects deterministic adapter failures for cleanup tests.
type FailurePlan struct {
	// Inventory makes every inventory call fail when set.
	Inventory bool
	// Quote makes every quote call fail when set.
	Quote bool
	// CreateAfterResources fails after retaining this many partial resources.
	CreateAfterResources int
	// DestroyRunIDs makes cleanup fail for the listed exact run IDs.
	DestroyRunIDs map[string]bool
}

// Options configures one isolated fake provider instance.
type Options struct {
	// Now supplies deterministic timestamps.
	Now func() time.Time
	// Quote overrides the default bounded price and capacity decision.
	Quote cloudsim.Quote
	// Failures configures deterministic failure injection.
	Failures FailurePlan
	// StatePath optionally identifies a persistent fake provider inventory file.
	StatePath string
}

// Provider is an in-memory, concurrency-safe fake Cloud Provider Adapter.
type Provider struct {
	mu        sync.RWMutex
	now       func() time.Time
	quote     cloudsim.Quote
	failures  FailurePlan
	runs      map[string]cloudsim.Run
	statePath string
}

// New creates an empty deterministic fake provider.
func New(opts Options) *Provider {
	return newProvider(opts)
}

// Open loads or creates a persistent fake provider inventory.
func Open(opts Options) (*Provider, error) {
	if strings.TrimSpace(opts.StatePath) == "" {
		return nil, fmt.Errorf("%w: empty path", ErrStateStore)
	}
	provider := newProvider(opts)
	file, err := os.Open(opts.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return provider, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", ErrStateStore, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateBytes+1))
	decoder.DisallowUnknownFields()
	var state persistentState
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrStateStore, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrStateStore)
	}
	if state.Schema != stateSchemaV1 || state.Runs == nil {
		return nil, fmt.Errorf("%w: unsupported schema", ErrStateStore)
	}
	for runID, run := range state.Runs {
		if runID == "" || run.ID != runID {
			return nil, fmt.Errorf("%w: invalid run identity", ErrStateStore)
		}
		provider.runs[runID] = cloneRun(run)
	}
	return provider, nil
}

func newProvider(opts Options) *Provider {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	quote := opts.Quote
	if quote.Currency == "" {
		quote = cloudsim.Quote{
			Currency:               defaultCurrency,
			WorstCaseCostMicros:    8_000_000,
			SelectedSKU:            "fake.small",
			SpotPriceMicrosPerHour: 1_000_000,
			CapacityAvailable:      true,
			QuotaAvailable:         true,
		}
	}
	return &Provider{now: now, quote: quote, failures: opts.Failures, runs: make(map[string]cloudsim.Run), statePath: opts.StatePath}
}

// Name returns the stable fake provider identifier.
func (*Provider) Name() string { return ProviderName }

// Authority derives the exact fake inventory binding and fails closed when the
// persistent store is empty or spans more than one account or region.
func (p *Provider) Authority(context.Context) (cloudsim.ProviderAuthority, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var authority cloudsim.ProviderAuthority
	for _, run := range p.runs {
		candidate := cloudsim.ProviderAuthority{Provider: ProviderName, Region: run.Region, AccountIDHash: run.AccountIDHash}
		if candidate.Region == "" || candidate.AccountIDHash == "" {
			return cloudsim.ProviderAuthority{}, ErrStateStore
		}
		if authority.Provider == "" {
			authority = candidate
			continue
		}
		if authority != candidate {
			return cloudsim.ProviderAuthority{}, ErrStateStore
		}
	}
	if authority.Provider == "" {
		return cloudsim.ProviderAuthority{}, ErrStateStore
	}
	return authority, nil
}

// Inventory returns deterministic deep copies of all known run records.
func (p *Provider) Inventory(context.Context) ([]cloudsim.Run, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.failures.Inventory {
		return nil, ErrInjectedFailure
	}
	ids := make([]string, 0, len(p.runs))
	for id := range p.runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	runs := make([]cloudsim.Run, 0, len(ids))
	for _, id := range ids {
		runs = append(runs, cloneRun(p.runs[id]))
	}
	return runs, nil
}

// Quote returns a bounded deterministic fake price and capacity decision.
func (p *Provider) Quote(context.Context, cloudsim.CreateRequest) (cloudsim.Quote, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.failures.Quote {
		return cloudsim.Quote{}, ErrInjectedFailure
	}
	return p.quote, nil
}

// Create builds one isolated run network, four compute hosts, four independent disks, and simulator ingress resources.
func (p *Provider) Create(_ context.Context, req cloudsim.CreateRequest, quote cloudsim.Quote) (cloudsim.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := validateMandatoryTags(req); err != nil {
		return cloudsim.Run{}, err
	}
	if existing, ok := p.runs[req.RunID]; ok && existing.State != cloudsim.StateReleased {
		return cloudsim.Run{}, fmt.Errorf("%w: run %s already exists", cloudsim.ErrActiveRunExists, req.RunID)
	}
	resources := fakeResources(req)
	run := cloudsim.Run{
		ID: req.RunID, Provider: ProviderName, Region: req.Region, AccountIDHash: req.AccountIDHash,
		Repository: req.Repository, State: cloudsim.StateProvisioning, CreatedAt: p.now().UTC(),
		ExpiresAt: req.ExpiresAt.UTC(), Tags: maps.Clone(req.Tags), Quote: quote,
	}
	if after := p.failures.CreateAfterResources; after > 0 && after < len(resources) {
		run.Resources = cloneResources(resources[:after])
		run.State = cloudsim.StateReleasePending
		p.runs[req.RunID] = run
		if err := p.persistLocked(); err != nil {
			return cloudsim.Run{}, err
		}
		return cloudsim.Run{}, fmt.Errorf("%w: create after %d resources", ErrInjectedFailure, after)
	}
	run.Resources = resources
	run.State = cloudsim.StateReady
	p.runs[req.RunID] = run
	if err := p.persistLocked(); err != nil {
		return cloudsim.Run{}, err
	}
	return cloneRun(run), nil
}

// Status returns one exact run record or ErrRunNotFound.
func (p *Provider) Status(_ context.Context, runID string) (cloudsim.Run, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	run, ok := p.runs[runID]
	if !ok {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	if run.State == cloudsim.StateRunning && !run.ActiveUntil.IsZero() && !p.now().UTC().Before(run.ActiveUntil) {
		run.State = cloudsim.StateAnalysisGrace
	}
	return cloneRun(run), nil
}

// Transition persists one control-plane-validated lifecycle step.
func (p *Provider) Transition(_ context.Context, req cloudsim.TransitionRequest) (cloudsim.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, ok := p.runs[req.RunID]
	if !ok {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	run.State = req.Next
	if req.Next == cloudsim.StateRunning {
		run.ActiveUntil = req.ActiveUntil.UTC()
	}
	p.runs[req.RunID] = run
	if err := p.persistLocked(); err != nil {
		return cloudsim.Run{}, err
	}
	return cloneRun(run), nil
}

// OpenDeployment records one simulator-only temporary SSH ingress window.
func (p *Provider) OpenDeployment(_ context.Context, req cloudsim.OpenDeploymentRequest) (cloudsim.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, ok := p.runs[req.RunID]
	if !ok {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	if run.State == cloudsim.StateReleased {
		return cloudsim.Run{}, cloudsim.ErrRunReleased
	}
	run.DeploymentWindow = &cloudsim.DeploymentWindow{SourcePrefix: req.SourcePrefix, Until: req.Until.UTC()}
	p.runs[req.RunID] = run
	if err := p.persistLocked(); err != nil {
		return cloudsim.Run{}, err
	}
	return cloneRun(run), nil
}

// CloseDeployment clears temporary simulator SSH ingress for one exact run.
func (p *Provider) CloseDeployment(_ context.Context, runID string) (cloudsim.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, ok := p.runs[runID]
	if !ok {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	run.DeploymentWindow = nil
	p.runs[runID] = run
	if err := p.persistLocked(); err != nil {
		return cloudsim.Run{}, err
	}
	return cloneRun(run), nil
}

// OpenAnalysis records one run-scoped temporary ingress window.
func (p *Provider) OpenAnalysis(_ context.Context, req cloudsim.OpenAnalysisRequest) (cloudsim.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, ok := p.runs[req.RunID]
	if !ok {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	if run.State == cloudsim.StateReleased {
		return cloudsim.Run{}, cloudsim.ErrRunReleased
	}
	run.AnalysisWindow = &cloudsim.AnalysisWindow{SourcePrefix: req.SourcePrefix, Until: req.Until.UTC()}
	p.runs[req.RunID] = run
	if err := p.persistLocked(); err != nil {
		return cloudsim.Run{}, err
	}
	return cloneRun(run), nil
}

// CloseAnalysis clears temporary ingress for one exact run.
func (p *Provider) CloseAnalysis(_ context.Context, runID string) (cloudsim.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, ok := p.runs[runID]
	if !ok {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	run.AnalysisWindow = nil
	p.runs[runID] = run
	if err := p.persistLocked(); err != nil {
		return cloudsim.Run{}, err
	}
	return cloneRun(run), nil
}

// Destroy clears all run resources and records provider-proven release.
func (p *Provider) Destroy(_ context.Context, runID string) (cloudsim.Run, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failures.DestroyRunIDs[runID] {
		return cloudsim.Run{}, ErrInjectedFailure
	}
	run, ok := p.runs[runID]
	if !ok {
		return cloudsim.Run{}, cloudsim.ErrRunNotFound
	}
	run.State = cloudsim.StateReleased
	run.Resources = []cloudsim.Resource{}
	run.AnalysisWindow = nil
	run.DeploymentWindow = nil
	p.runs[runID] = run
	if err := p.persistLocked(); err != nil {
		return cloudsim.Run{}, err
	}
	return cloneRun(run), nil
}

type persistentState struct {
	Schema string                  `json:"schema"`
	Runs   map[string]cloudsim.Run `json:"runs"`
}

func (p *Provider) persistLocked() error {
	if strings.TrimSpace(p.statePath) == "" {
		return nil
	}
	runs := make(map[string]cloudsim.Run, len(p.runs))
	for runID, run := range p.runs {
		runs[runID] = cloneRun(run)
	}
	data, err := json.Marshal(persistentState{Schema: stateSchemaV1, Runs: runs})
	if err != nil {
		return fmt.Errorf("%w: encode: %v", ErrStateStore, err)
	}
	if len(data) > maxStateBytes {
		return fmt.Errorf("%w: inventory exceeds %d bytes", ErrStateStore, maxStateBytes)
	}
	dir := filepath.Dir(p.statePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w: create directory: %v", ErrStateStore, err)
	}
	temp, err := os.CreateTemp(dir, ".fake-inventory-*")
	if err != nil {
		return fmt.Errorf("%w: create temp: %v", ErrStateStore, err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("%w: chmod: %v", ErrStateStore, err)
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return fmt.Errorf("%w: write: %v", ErrStateStore, err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("%w: sync: %v", ErrStateStore, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("%w: close: %v", ErrStateStore, err)
	}
	if err := os.Rename(tempPath, p.statePath); err != nil {
		return fmt.Errorf("%w: replace: %v", ErrStateStore, err)
	}
	return nil
}

func validateMandatoryTags(req cloudsim.CreateRequest) error {
	for _, key := range cloudsim.MandatoryTagKeys() {
		if strings.TrimSpace(req.Tags[key]) == "" {
			return fmt.Errorf("%w: %s", ErrInvalidTags, key)
		}
	}
	return nil
}

func fakeResources(req cloudsim.CreateRequest) []cloudsim.Resource {
	type resourceDef struct {
		kind     string
		role     string
		billable bool
	}
	defs := []resourceDef{
		{kind: "vpc", role: "run-network"},
		{kind: "subnet", role: "run-network"},
		{kind: "security-group", role: "run-network"},
		{kind: "compute", role: "node-1", billable: true},
		{kind: "disk", role: "node-1", billable: true},
		{kind: "compute", role: "node-2", billable: true},
		{kind: "disk", role: "node-2", billable: true},
		{kind: "compute", role: "node-3", billable: true},
		{kind: "disk", role: "node-3", billable: true},
		{kind: "compute", role: "sim", billable: true},
		{kind: "disk", role: "sim", billable: true},
		{kind: "public-address", role: "sim", billable: true},
	}
	resources := make([]cloudsim.Resource, 0, len(defs))
	for index, def := range defs {
		tags := maps.Clone(req.Tags)
		tags[cloudsim.TagResourceRole] = def.role
		resources = append(resources, cloudsim.Resource{
			ID:   fmt.Sprintf("%s/%02d/%s/%s", req.RunID, index+1, def.kind, def.role),
			Kind: def.kind, Role: def.role, Billable: def.billable, Tags: tags,
		})
	}
	return resources
}

func cloneRun(run cloudsim.Run) cloudsim.Run {
	run.Tags = maps.Clone(run.Tags)
	run.Resources = cloneResources(run.Resources)
	if run.AnalysisWindow != nil {
		window := *run.AnalysisWindow
		run.AnalysisWindow = &window
	}
	if run.DeploymentWindow != nil {
		window := *run.DeploymentWindow
		run.DeploymentWindow = &window
	}
	return run
}

func cloneResources(resources []cloudsim.Resource) []cloudsim.Resource {
	if resources == nil {
		return nil
	}
	cloned := make([]cloudsim.Resource, len(resources))
	for index, resource := range resources {
		cloned[index] = resource
		cloned[index].Tags = maps.Clone(resource.Tags)
	}
	return cloned
}
