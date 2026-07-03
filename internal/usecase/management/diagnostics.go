package management

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

const (
	defaultDiagnosticsLimit = 100
	maxDiagnosticsLimit     = 500
	diagnosticsScopeCluster = "cluster"
	diagnosticsScopeLocal   = "local_node"
)

var managerDiagnosticsNodeTimeout = 2 * time.Second

type diagnosticsTarget struct {
	nodeID  uint64
	skipped bool
	notes   []string
}

// DiagnosticsQueryRequest describes a manager diagnostics lookup across one or more nodes.
type DiagnosticsQueryRequest struct {
	// NodeID restricts the lookup to one node when non-zero.
	NodeID uint64
	// Query is forwarded to each node-local diagnostics reader after limit normalization.
	Query diagnostics.Query
}

// DiagnosticsStatus summarizes the aggregated diagnostics query result.
type DiagnosticsStatus string

const (
	DiagnosticsStatusOK       DiagnosticsStatus = "ok"
	DiagnosticsStatusError    DiagnosticsStatus = "error"
	DiagnosticsStatusTimeout  DiagnosticsStatus = "timeout"
	DiagnosticsStatusPartial  DiagnosticsStatus = "partial"
	DiagnosticsStatusNotFound DiagnosticsStatus = "not_found"
)

// DiagnosticsQueryResponse contains merged node diagnostics events and aggregate hints.
type DiagnosticsQueryResponse struct {
	Scope       string
	Status      DiagnosticsStatus
	GeneratedAt time.Time
	Query       diagnostics.Query
	Summary     DiagnosticsSummary
	Nodes       []DiagnosticsNodeResult
	Events      []DiagnosticsEvent
	Notes       []string
}

// DiagnosticsSummary contains compact fields used by manager diagnostics overviews.
type DiagnosticsSummary struct {
	FirstFailureStage     string
	FirstFailureResult    string
	FirstFailureErrorCode string
	SlowestStage          string
	SlowestDurationMS     int64
	InvolvedNodes         []uint64
	PeerNodes             []uint64
	SlotID                uint32
	ChannelKey            string
	ClientMsgNo           string
	MessageSeq            uint64
	EventCount            int
}

// DiagnosticsNodeResult reports one node's contribution to the aggregate response.
type DiagnosticsNodeResult struct {
	NodeID     uint64
	Status     string
	DurationMS int64
	EventCount int
	Notes      []string
}

// DiagnosticsEvent is the manager-facing diagnostics event DTO.
type DiagnosticsEvent struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Stage        string
	At           time.Time
	DurationMS   int64
	NodeID       uint64
	PeerNodeID   uint64
	SlotID       uint32
	ChannelKey   string
	ClientMsgNo  string
	MessageSeq   uint64
	RangeStart   uint64
	RangeEnd     uint64
	Service      string
	Result       string
	ErrorCode    string
	Error        string
	Attempt      int
	RequestCount int
	RecordCount  int
	ByteCount    int
	QueueDepth   int
	ReplicaRole  string
	SampleReason string
}

// QueryDiagnostics aggregates retained diagnostics events from the requested manager scope.
func (a *App) QueryDiagnostics(ctx context.Context, req DiagnosticsQueryRequest) (DiagnosticsQueryResponse, error) {
	query := normalizeManagerDiagnosticsQuery(req.Query)
	scope, targets, notes := a.diagnosticsTargets(ctx, req.NodeID)

	nodes := make([]DiagnosticsNodeResult, 0, len(targets))
	queryTargets := make([]diagnosticsTarget, 0, len(targets))
	for _, target := range targets {
		if target.skipped {
			nodes = append(nodes, DiagnosticsNodeResult{NodeID: target.nodeID, Status: "skipped", Notes: append([]string(nil), target.notes...)})
			continue
		}
		queryTargets = append(queryTargets, target)
	}

	queryResults := a.queryDiagnosticsTargets(ctx, queryTargets, query)
	nodes = append(nodes, queryResults.nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })

	events := truncateDiagnosticsEvents(queryResults.events, query.Limit)
	summary := managerDiagnosticsSummary(events)
	status := managerDiagnosticsStatus(events, nodes)
	if managerDiagnosticsSnapshotUnavailable(notes) && (status == DiagnosticsStatusOK || status == DiagnosticsStatusNotFound) {
		status = DiagnosticsStatusPartial
	}
	return DiagnosticsQueryResponse{
		Scope:       scope,
		Status:      status,
		GeneratedAt: a.now(),
		Query:       query,
		Summary:     summary,
		Nodes:       nodes,
		Events:      events,
		Notes:       notes,
	}, nil
}

type diagnosticsQueryResults struct {
	nodes  []DiagnosticsNodeResult
	events []DiagnosticsEvent
}

func (a *App) queryDiagnosticsTargets(ctx context.Context, targets []diagnosticsTarget, query diagnostics.Query) diagnosticsQueryResults {
	if len(targets) == 0 {
		return diagnosticsQueryResults{}
	}
	sem := make(chan struct{}, 8)
	results := make(chan diagnosticsQueryResults, len(targets))
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results <- a.queryDiagnosticsTarget(ctx, target, query)
		}()
	}
	wg.Wait()
	close(results)

	out := diagnosticsQueryResults{}
	for result := range results {
		out.nodes = append(out.nodes, result.nodes...)
		out.events = append(out.events, result.events...)
	}
	sort.Slice(out.events, func(i, j int) bool { return out.events[i].At.Before(out.events[j].At) })
	return out
}

func (a *App) queryDiagnosticsTarget(ctx context.Context, target diagnosticsTarget, query diagnostics.Query) diagnosticsQueryResults {
	if a.diagnostics == nil {
		return unavailableDiagnosticsTarget(target, "diagnostics reader is unavailable")
	}
	nodeCtx, cancel := context.WithTimeout(ctx, managerDiagnosticsNodeTimeout)
	defer cancel()
	result, err := a.diagnostics.QueryNodeDiagnostics(nodeCtx, target.nodeID, query)
	if err != nil {
		note := "diagnostics query unavailable: " + err.Error()
		if errors.Is(nodeCtx.Err(), context.DeadlineExceeded) {
			note = "diagnostics query timed out"
		}
		return unavailableDiagnosticsTarget(target, note)
	}
	events := make([]DiagnosticsEvent, 0, len(result.Events))
	for _, event := range result.Events {
		events = append(events, managerDiagnosticsEvent(event))
	}
	nodeStatus := string(result.Status)
	if nodeStatus == "" {
		nodeStatus = string(diagnostics.StatusNotFound)
		if len(events) > 0 {
			nodeStatus = string(diagnostics.StatusOK)
		}
	}
	return diagnosticsQueryResults{
		nodes: []DiagnosticsNodeResult{{
			NodeID:     target.nodeID,
			Status:     nodeStatus,
			DurationMS: result.DurationMS,
			EventCount: len(events),
			Notes:      append([]string(nil), result.Notes...),
		}},
		events: events,
	}
}

func unavailableDiagnosticsTarget(target diagnosticsTarget, note string) diagnosticsQueryResults {
	return diagnosticsQueryResults{nodes: []DiagnosticsNodeResult{{
		NodeID: target.nodeID,
		Status: "unavailable",
		Notes:  []string{note},
	}}}
}

func normalizeManagerDiagnosticsQuery(query diagnostics.Query) diagnostics.Query {
	if query.Limit <= 0 {
		query.Limit = defaultDiagnosticsLimit
	}
	if query.Limit > maxDiagnosticsLimit {
		query.Limit = maxDiagnosticsLimit
	}
	return query
}

func (a *App) diagnosticsTargets(ctx context.Context, requested uint64) (scope string, targets []diagnosticsTarget, notes []string) {
	if requested != 0 {
		return diagnosticsScopeLocal, []diagnosticsTarget{{nodeID: requested}}, nil
	}
	if a.cluster == nil {
		return diagnosticsScopeLocal, []diagnosticsTarget{{nodeID: 0}}, []string{"controller snapshot is unavailable; querying local node only"}
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return diagnosticsScopeLocal, []diagnosticsTarget{{nodeID: a.cluster.NodeID()}}, []string{"controller snapshot is unavailable; querying local node only"}
	}
	targets = make([]diagnosticsTarget, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		switch node.Status {
		case control.NodeAlive, control.NodeSuspect:
			if node.NodeID != 0 {
				targets = append(targets, diagnosticsTarget{nodeID: node.NodeID})
			}
		case control.NodeDown:
			targets = append(targets, diagnosticsTarget{nodeID: node.NodeID, skipped: true, notes: []string{"down node is skipped"}})
		}
	}
	if len(targets) == 0 {
		targets = append(targets, diagnosticsTarget{nodeID: a.cluster.NodeID()})
		notes = append(notes, "controller snapshot contains no eligible nodes; querying local node only")
		return diagnosticsScopeLocal, targets, notes
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].nodeID < targets[j].nodeID })
	return diagnosticsScopeCluster, targets, nil
}

func managerDiagnosticsEvent(event diagnostics.Event) DiagnosticsEvent {
	return DiagnosticsEvent{
		TraceID:      event.TraceID,
		SpanID:       event.SpanID,
		ParentSpanID: event.ParentSpanID,
		Stage:        string(event.Stage),
		At:           event.At,
		DurationMS:   event.Duration.Milliseconds(),
		NodeID:       event.NodeID,
		PeerNodeID:   event.PeerNodeID,
		SlotID:       event.SlotID,
		ChannelKey:   event.ChannelKey,
		ClientMsgNo:  event.ClientMsgNo,
		MessageSeq:   event.MessageSeq,
		RangeStart:   event.RangeStart,
		RangeEnd:     event.RangeEnd,
		Service:      event.Service,
		Result:       string(event.Result),
		ErrorCode:    string(event.ErrorCode),
		Error:        event.Error,
		Attempt:      event.Attempt,
		RequestCount: event.RequestCount,
		RecordCount:  event.RecordCount,
		ByteCount:    event.ByteCount,
		QueueDepth:   event.QueueDepth,
		ReplicaRole:  event.ReplicaRole,
		SampleReason: event.SampleReason,
	}
}

func managerDiagnosticsSnapshotUnavailable(notes []string) bool {
	for _, note := range notes {
		if note == "controller snapshot is unavailable; querying local node only" {
			return true
		}
	}
	return false
}

func managerDiagnosticsStatus(events []DiagnosticsEvent, nodes []DiagnosticsNodeResult) DiagnosticsStatus {
	for _, event := range events {
		if event.Result == string(diagnostics.ResultError) {
			return DiagnosticsStatusError
		}
	}
	for _, event := range events {
		if event.Result == string(diagnostics.ResultTimeout) {
			return DiagnosticsStatusTimeout
		}
	}
	for _, event := range events {
		switch event.Result {
		case string(diagnostics.ResultPartial), string(diagnostics.ResultDropped), string(diagnostics.ResultCanceled):
			return DiagnosticsStatusPartial
		}
	}
	for _, node := range nodes {
		switch node.Status {
		case "unavailable", "skipped":
			return DiagnosticsStatusPartial
		}
	}
	if len(events) == 0 {
		return DiagnosticsStatusNotFound
	}
	return DiagnosticsStatusOK
}

func managerDiagnosticsSummary(events []DiagnosticsEvent) DiagnosticsSummary {
	summary := DiagnosticsSummary{EventCount: len(events)}
	involved := map[uint64]struct{}{}
	peers := map[uint64]struct{}{}
	for _, event := range events {
		if event.NodeID != 0 {
			involved[event.NodeID] = struct{}{}
		}
		if event.PeerNodeID != 0 {
			peers[event.PeerNodeID] = struct{}{}
		}
		if summary.FirstFailureStage == "" {
			switch event.Result {
			case string(diagnostics.ResultError), string(diagnostics.ResultTimeout), string(diagnostics.ResultCanceled):
				summary.FirstFailureStage = event.Stage
				summary.FirstFailureResult = event.Result
				summary.FirstFailureErrorCode = event.ErrorCode
			}
		}
		if event.DurationMS > summary.SlowestDurationMS {
			summary.SlowestDurationMS = event.DurationMS
			summary.SlowestStage = event.Stage
		}
		if summary.SlotID == 0 && event.SlotID != 0 {
			summary.SlotID = event.SlotID
		}
		if summary.ChannelKey == "" && event.ChannelKey != "" {
			summary.ChannelKey = event.ChannelKey
		}
		if summary.ClientMsgNo == "" && event.ClientMsgNo != "" {
			summary.ClientMsgNo = event.ClientMsgNo
		}
		if summary.MessageSeq == 0 && event.MessageSeq != 0 {
			summary.MessageSeq = event.MessageSeq
		}
	}
	summary.InvolvedNodes = sortedUint64Keys(involved)
	summary.PeerNodes = sortedUint64Keys(peers)
	return summary
}

func truncateDiagnosticsEvents(events []DiagnosticsEvent, limit int) []DiagnosticsEvent {
	if limit <= 0 {
		limit = defaultDiagnosticsLimit
	}
	if limit > maxDiagnosticsLimit {
		limit = maxDiagnosticsLimit
	}
	if len(events) <= limit {
		return events
	}
	return events[len(events)-limit:]
}

func sortedUint64Keys(values map[uint64]struct{}) []uint64 {
	out := make([]uint64, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
