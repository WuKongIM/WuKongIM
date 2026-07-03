package app

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
)

func (a *App) diagnosticsQuery(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult {
	if a == nil || a.diagnostics == nil {
		var nodeID uint64
		if a != nil {
			nodeID = a.cfg.Node.ID
		}
		return diagnostics.QueryResult{
			Scope:       "local_node",
			NodeID:      nodeID,
			Query:       query,
			TraceID:     query.TraceID,
			ClientMsgNo: query.ClientMsgNo,
			ChannelKey:  query.ChannelKey,
			MessageSeq:  query.MessageSeq,
			Status:      diagnostics.StatusNotFound,
			Events:      []diagnostics.Event{},
			Notes:       []string{"diagnostics store is disabled on this node"},
		}
	}
	return a.diagnostics.Query(ctx, query)
}

// QueryDiagnostics returns the local-node diagnostics query result for the supplied lookup.
func (a *App) QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult {
	return a.diagnosticsQuery(ctx, query)
}

// AddDiagnosticsTrackingRule installs one local-node diagnostics tracking rule.
func (a *App) AddDiagnosticsTrackingRule(_ context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	if a == nil || a.diagnosticsTracking == nil {
		return diagnostics.TrackingRule{}, fmt.Errorf("app: diagnostics tracking not configured")
	}
	return a.diagnosticsTracking.Add(input)
}

// ListDiagnosticsTrackingRules returns active local-node diagnostics tracking rules.
func (a *App) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
	if a == nil || a.diagnosticsTracking == nil {
		return nil, fmt.Errorf("app: diagnostics tracking not configured")
	}
	return a.diagnosticsTracking.List(), nil
}

// DeleteDiagnosticsTrackingRule removes one local-node diagnostics tracking rule.
func (a *App) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
	if a == nil || a.diagnosticsTracking == nil {
		return fmt.Errorf("app: diagnostics tracking not configured")
	}
	a.diagnosticsTracking.Delete(ruleID)
	return nil
}

type managementDiagnosticsLocal interface {
	QueryDiagnostics(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult
}

type managementDiagnosticsRemote interface {
	QueryDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error)
}

// managementDiagnosticsReader routes manager diagnostics reads to local app state or node RPC.
type managementDiagnosticsReader struct {
	// localNodeID identifies the current process for local short-circuiting.
	localNodeID uint64
	// local reads the current node's bounded diagnostics store.
	local managementDiagnosticsLocal
	// remote reads another node's diagnostics store through node RPC.
	remote managementDiagnosticsRemote
}

// QueryNodeDiagnostics queries exactly the requested node without falling back to local data for remote nodes.
func (r managementDiagnosticsReader) QueryNodeDiagnostics(ctx context.Context, nodeID uint64, query diagnostics.Query) (diagnostics.QueryResult, error) {
	if nodeID == r.localNodeID {
		if r.local == nil {
			return diagnostics.QueryResult{}, fmt.Errorf("app: local diagnostics not configured")
		}
		return r.local.QueryDiagnostics(ctx, query), nil
	}
	if r.remote == nil {
		return diagnostics.QueryResult{}, fmt.Errorf("app: diagnostics node client not configured")
	}
	return r.remote.QueryDiagnostics(ctx, nodeID, query)
}

type managementDiagnosticsTrackingLocal interface {
	AddDiagnosticsTrackingRule(ctx context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
	ListDiagnosticsTrackingRules(ctx context.Context) ([]diagnostics.TrackingRule, error)
	DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) error
}

type managementDiagnosticsTrackingRemote interface {
	AddDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
	ListDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error)
	DeleteDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error
}

// managementDiagnosticsTrackingReader routes diagnostics tracking mutations to local app state or node RPC.
type managementDiagnosticsTrackingReader struct {
	// localNodeID identifies the current process for local short-circuiting.
	localNodeID uint64
	// local mutates the current node's runtime diagnostics tracking rules.
	local managementDiagnosticsTrackingLocal
	// remote mutates another node's runtime diagnostics tracking rules through node RPC.
	remote managementDiagnosticsTrackingRemote
}

// AddNodeDiagnosticsTrackingRule installs a diagnostics tracking rule on exactly one requested node.
func (r managementDiagnosticsTrackingReader) AddNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
	if nodeID == r.localNodeID {
		if r.local == nil {
			return diagnostics.TrackingRule{}, fmt.Errorf("app: local diagnostics tracking not configured")
		}
		return r.local.AddDiagnosticsTrackingRule(ctx, input)
	}
	if r.remote == nil {
		return diagnostics.TrackingRule{}, fmt.Errorf("app: diagnostics tracking node client not configured")
	}
	return r.remote.AddDiagnosticsTrackingRule(ctx, nodeID, input)
}

// ListNodeDiagnosticsTrackingRules returns tracking rules from exactly one requested node.
func (r managementDiagnosticsTrackingReader) ListNodeDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error) {
	if nodeID == r.localNodeID {
		if r.local == nil {
			return nil, fmt.Errorf("app: local diagnostics tracking not configured")
		}
		return r.local.ListDiagnosticsTrackingRules(ctx)
	}
	if r.remote == nil {
		return nil, fmt.Errorf("app: diagnostics tracking node client not configured")
	}
	return r.remote.ListDiagnosticsTrackingRules(ctx, nodeID)
}

// DeleteNodeDiagnosticsTrackingRule removes a diagnostics tracking rule from exactly one requested node.
func (r managementDiagnosticsTrackingReader) DeleteNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error {
	if nodeID == r.localNodeID {
		if r.local == nil {
			return fmt.Errorf("app: local diagnostics tracking not configured")
		}
		return r.local.DeleteDiagnosticsTrackingRule(ctx, ruleID)
	}
	if r.remote == nil {
		return fmt.Errorf("app: diagnostics tracking node client not configured")
	}
	return r.remote.DeleteDiagnosticsTrackingRule(ctx, nodeID, ruleID)
}

func diagnosticsStoreOptions(cfg Config) diagnostics.StoreOptions {
	return diagnostics.StoreOptions{
		NodeID:   cfg.Node.ID,
		Capacity: cfg.Observability.Diagnostics.BufferSize,
	}
}

func diagnosticsSamplerOptions(cfg Config) diagnostics.SamplerOptions {
	debugMatches := make([]diagnostics.DebugMatch, 0, len(cfg.Observability.Diagnostics.DebugMatches))
	for _, match := range cfg.Observability.Diagnostics.DebugMatches {
		debugMatches = append(debugMatches, diagnostics.DebugMatch{
			UID:         match.UID,
			ChannelKey:  match.ChannelKey,
			ClientMsgNo: match.ClientMsgNo,
			TraceID:     match.TraceID,
			TTL:         time.Duration(match.TTLSeconds) * time.Second,
			SampleRate:  match.SampleRate,
		})
	}
	return diagnostics.SamplerOptions{
		SampleRate:         cfg.Observability.Diagnostics.SampleRate,
		SlowThreshold:      cfg.Observability.Diagnostics.SlowThreshold,
		ErrorSampleRate:    cfg.Observability.Diagnostics.ErrorSampleRate,
		ErrorSampleRateSet: cfg.Observability.Diagnostics.errorSampleRateSet,
		DebugMatches:       debugMatches,
	}
}

func (a *App) restoreDiagnosticsSink() {
	if a == nil || a.diagnosticsRestore == nil {
		return
	}
	a.diagnosticsRestore()
	a.diagnosticsRestore = nil
}
