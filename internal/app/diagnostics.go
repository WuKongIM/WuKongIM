package app

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func (a *App) diagnosticsQuery(ctx context.Context, query diagnostics.Query) diagnostics.QueryResult {
	if a == nil || a.diagnostics == nil {
		var nodeID uint64
		if a != nil {
			nodeID = diagnosticsNodeID(a.cfg)
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
		return diagnostics.TrackingRule{}, fmt.Errorf("internal/app: diagnostics tracking not configured")
	}
	return a.diagnosticsTracking.Add(input)
}

// ListDiagnosticsTrackingRules returns active local-node diagnostics tracking rules.
func (a *App) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
	if a == nil || a.diagnosticsTracking == nil {
		return nil, fmt.Errorf("internal/app: diagnostics tracking not configured")
	}
	return a.diagnosticsTracking.List(), nil
}

// DeleteDiagnosticsTrackingRule removes one local-node diagnostics tracking rule.
func (a *App) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
	if a == nil || a.diagnosticsTracking == nil {
		return fmt.Errorf("internal/app: diagnostics tracking not configured")
	}
	a.diagnosticsTracking.Delete(ruleID)
	return nil
}

func diagnosticsStoreOptions(cfg Config) diagnostics.StoreOptions {
	return diagnostics.StoreOptions{
		NodeID:   diagnosticsNodeID(cfg),
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
		SampleRate:           cfg.Observability.Diagnostics.SampleRate,
		SlowThreshold:        cfg.Observability.Diagnostics.SlowThreshold,
		ErrorSampleRate:      cfg.Observability.Diagnostics.ErrorSampleRate,
		ErrorSampleRateSet:   cfg.Observability.diagnosticsErrorSampleRateSet,
		DeepSampleRate:       cfg.Observability.Diagnostics.DeepSampleRate,
		DeepSlowThreshold:    cfg.Observability.Diagnostics.DeepSlowThreshold,
		DeepMaxItemsPerBatch: cfg.Observability.Diagnostics.DeepMaxItemsPerBatch,
		DebugMatches:         debugMatches,
	}
}

func diagnosticsNodeID(cfg Config) uint64 {
	if cfg.Cluster.NodeID != 0 {
		return cfg.Cluster.NodeID
	}
	return cfg.NodeID
}

func (a *App) restoreDiagnosticsSink() {
	if a == nil || a.diagnosticsRestore == nil {
		return
	}
	a.diagnosticsRestore()
	a.diagnosticsRestore = nil
}
