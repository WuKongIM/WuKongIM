package app

import (
	"context"
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
