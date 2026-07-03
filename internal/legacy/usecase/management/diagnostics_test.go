package management

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/observability/diagnostics"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/stretchr/testify/require"
)

type fakeDiagnosticsReader struct {
	mu      sync.Mutex
	results map[uint64]diagnostics.QueryResult
	errors  map[uint64]error
	queries map[uint64]diagnostics.Query
}

func (f *fakeDiagnosticsReader) QueryNodeDiagnostics(_ context.Context, nodeID uint64, q diagnostics.Query) (diagnostics.QueryResult, error) {
	f.mu.Lock()
	if f.queries == nil {
		f.queries = map[uint64]diagnostics.Query{}
	}
	f.queries[nodeID] = q
	f.mu.Unlock()
	if err := f.errors[nodeID]; err != nil {
		return diagnostics.QueryResult{}, err
	}
	return f.results[nodeID], nil
}

func TestQueryDiagnosticsAggregatesSuccessfulNodes(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		1: diagnosticsResult(1, diagnostics.StatusOK,
			diagnosticsEvent(1, now.Add(3*time.Second), "replica", diagnostics.ResultOK),
			diagnosticsEvent(1, now.Add(time.Second), "gateway", diagnostics.ResultOK),
		),
		2: diagnosticsResult(2, diagnostics.StatusOK,
			diagnosticsEvent(2, now.Add(2*time.Second), "channel", diagnostics.ResultOK),
		),
	}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.Equal(t, DiagnosticsStatusOK, got.Status)
	require.Equal(t, []uint64{1, 2}, got.Summary.InvolvedNodes)
	require.Len(t, got.Events, 3)
	require.Equal(t, []string{"gateway", "channel", "replica"}, diagnosticsStages(got.Events))
	require.Equal(t, 3, got.Summary.EventCount)
}

func TestQueryDiagnosticsReturnsNotFoundWhenAllNodesAreEmpty(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		1: diagnosticsResult(1, diagnostics.StatusNotFound),
		2: diagnosticsResult(2, diagnostics.StatusNotFound),
	}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.Equal(t, DiagnosticsStatusNotFound, got.Status)
	require.Empty(t, got.Events)
}

func TestQueryDiagnosticsQueriesAliveSuspectAndDrainingNodes(t *testing.T) {
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		1: diagnosticsResult(1, diagnostics.StatusNotFound),
		2: diagnosticsResult(2, diagnostics.StatusNotFound),
		3: diagnosticsResult(3, diagnostics.StatusNotFound),
	}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusSuspect},
			{NodeID: 3, Status: controllermeta.NodeStatusDraining},
			{NodeID: 4, Status: controllermeta.NodeStatusDead},
		}},
		Diagnostics: reader,
	})

	_, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.ElementsMatch(t, []uint64{1, 2, 3}, queriedNodeIDs(reader))
}

func TestQueryDiagnosticsSkipsDeadNodesAndMarksPartial(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		1: diagnosticsResult(1, diagnostics.StatusOK, diagnosticsEvent(1, now, "gateway", diagnostics.ResultOK)),
	}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusDead},
		}},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.Equal(t, DiagnosticsStatusPartial, got.Status)
	require.Equal(t, "skipped", diagnosticsNodeStatus(got.Nodes, 2))
}

func TestQueryDiagnosticsReturnsPartialWhenRemoteNodeFails(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{
		results: map[uint64]diagnostics.QueryResult{1: diagnosticsResult(1, diagnostics.StatusOK, diagnosticsEvent(1, now, "gateway", diagnostics.ResultOK))},
		errors:  map[uint64]error{2: errors.New("rpc unavailable")},
	}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.Equal(t, DiagnosticsStatusPartial, got.Status)
	require.Len(t, got.Events, 1)
	require.Equal(t, "unavailable", diagnosticsNodeStatus(got.Nodes, 2))
}

func TestQueryDiagnosticsReturnsPartialWhenAllTargetsUnavailable(t *testing.T) {
	reader := &fakeDiagnosticsReader{errors: map[uint64]error{1: errors.New("down"), 2: errors.New("down")}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		Diagnostics: reader,
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.Equal(t, DiagnosticsStatusPartial, got.Status)
	require.Empty(t, got.Events)
	for _, node := range got.Nodes {
		require.Equal(t, "unavailable", node.Status)
	}
}

func TestQueryDiagnosticsStatusPrefersErrorThenTimeoutThenPartial(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		events []DiagnosticsEvent
		nodes  []DiagnosticsNodeResult
		want   DiagnosticsStatus
	}{
		{name: "error beats timeout", events: []DiagnosticsEvent{{At: now, Result: string(diagnostics.ResultTimeout)}, {At: now, Result: string(diagnostics.ResultError)}}, want: DiagnosticsStatusError},
		{name: "timeout beats partial", events: []DiagnosticsEvent{{At: now, Result: string(diagnostics.ResultPartial)}, {At: now, Result: string(diagnostics.ResultTimeout)}}, want: DiagnosticsStatusTimeout},
		{name: "partial beats ok", events: []DiagnosticsEvent{{At: now, Result: string(diagnostics.ResultOK)}, {At: now, Result: string(diagnostics.ResultDropped)}}, want: DiagnosticsStatusPartial},
		{name: "unavailable beats ok", events: []DiagnosticsEvent{{At: now, Result: string(diagnostics.ResultOK)}}, nodes: []DiagnosticsNodeResult{{NodeID: 1, Status: "ok"}, {NodeID: 2, Status: "unavailable"}}, want: DiagnosticsStatusPartial},
		{name: "ok beats not found", events: []DiagnosticsEvent{{At: now, Result: string(diagnostics.ResultOK)}}, nodes: []DiagnosticsNodeResult{{NodeID: 1, Status: "ok"}}, want: DiagnosticsStatusOK},
		{name: "empty is not found", want: DiagnosticsStatusNotFound},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, managerDiagnosticsStatus(tt.events, tt.nodes))
		})
	}
}

func TestQueryDiagnosticsPropagatesResultFilterToNodeQueries(t *testing.T) {
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		1: diagnosticsResult(1, diagnostics.StatusNotFound),
		2: diagnosticsResult(2, diagnostics.StatusNotFound),
	}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		}},
		Diagnostics: reader,
	})

	_, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{Query: diagnostics.Query{Result: diagnostics.ResultError}})
	require.NoError(t, err)
	for _, q := range reader.queries {
		require.Equal(t, diagnostics.ResultError, q.Result)
	}
}

func TestQueryDiagnosticsUsesLocalNodeWhenControllerSnapshotUnavailable(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		9: diagnosticsResult(9, diagnostics.StatusNotFound),
	}}
	app := New(Options{
		LocalNodeID: 9,
		Cluster:     fakeClusterReader{listNodesErr: errors.New("controller unavailable")},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.Equal(t, "local_node", got.Scope)
	require.Equal(t, DiagnosticsStatusPartial, got.Status)
	require.Equal(t, []uint64{9}, queriedNodeIDs(reader))
	require.True(t, diagnosticsNotesContain(got.Notes, "controller snapshot is unavailable"), got.Notes)
}

func TestQueryDiagnosticsKeepsFallbackPartialWhenLocalNodeHasOKEvent(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		9: diagnosticsResult(9, diagnostics.StatusOK, diagnosticsEvent(9, now, "gateway", diagnostics.ResultOK)),
	}}
	app := New(Options{
		LocalNodeID: 9,
		Cluster:     fakeClusterReader{listNodesErr: errors.New("controller unavailable")},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{})
	require.NoError(t, err)
	require.Equal(t, "local_node", got.Scope)
	require.Equal(t, DiagnosticsStatusPartial, got.Status)
	require.Len(t, got.Events, 1)
	require.True(t, diagnosticsNotesContain(got.Notes, "controller snapshot is unavailable"), got.Notes)
}

func TestQueryDiagnosticsTruncatesMergedEventsToLimit(t *testing.T) {
	now := time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)
	reader := &fakeDiagnosticsReader{results: map[uint64]diagnostics.QueryResult{
		1: diagnosticsResult(1, diagnostics.StatusOK, diagnosticsEvent(1, now.Add(time.Second), "one", diagnostics.ResultOK), diagnosticsEvent(1, now.Add(4*time.Second), "four", diagnostics.ResultOK)),
		2: diagnosticsResult(2, diagnostics.StatusOK, diagnosticsEvent(2, now.Add(2*time.Second), "two", diagnostics.ResultOK)),
		3: diagnosticsResult(3, diagnostics.StatusOK, diagnosticsEvent(3, now.Add(3*time.Second), "three", diagnostics.ResultOK)),
	}}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Status: controllermeta.NodeStatusAlive},
			{NodeID: 3, Status: controllermeta.NodeStatusAlive},
		}},
		Diagnostics: reader,
		Now:         func() time.Time { return now },
	})

	got, err := app.QueryDiagnostics(context.Background(), DiagnosticsQueryRequest{Query: diagnostics.Query{Limit: 2}})
	require.NoError(t, err)
	require.Equal(t, []string{"three", "four"}, diagnosticsStages(got.Events))
	require.Equal(t, 2, got.Summary.EventCount)
}

func diagnosticsResult(nodeID uint64, status diagnostics.Status, events ...diagnostics.Event) diagnostics.QueryResult {
	return diagnostics.QueryResult{NodeID: nodeID, Status: status, Events: events, DurationMS: int64(nodeID)}
}

func diagnosticsEvent(nodeID uint64, at time.Time, stage string, result diagnostics.Result) diagnostics.Event {
	return diagnostics.Event{
		TraceID:     "trace",
		SpanID:      stage + "-span",
		Stage:       diagnostics.Stage(stage),
		At:          at,
		Duration:    time.Duration(nodeID) * time.Millisecond,
		NodeID:      nodeID,
		PeerNodeID:  nodeID + 10,
		SlotID:      uint32(nodeID),
		ChannelKey:  "ch",
		ClientMsgNo: "client",
		MessageSeq:  uint64(nodeID * 100),
		Service:     "svc",
		Result:      result,
	}
}

func diagnosticsStages(events []DiagnosticsEvent) []string {
	stages := make([]string, 0, len(events))
	for _, event := range events {
		stages = append(stages, event.Stage)
	}
	return stages
}

func diagnosticsNodeStatus(nodes []DiagnosticsNodeResult, nodeID uint64) string {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node.Status
		}
	}
	return ""
}

func queriedNodeIDs(reader *fakeDiagnosticsReader) []uint64 {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	ids := make([]uint64, 0, len(reader.queries))
	for nodeID := range reader.queries {
		ids = append(ids, nodeID)
	}
	return ids
}

func diagnosticsNotesContain(notes []string, needle string) bool {
	for _, note := range notes {
		if strings.Contains(note, needle) {
			return true
		}
	}
	return false
}
