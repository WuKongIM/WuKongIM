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
