package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeTopProvider struct {
	snapshot TopSnapshot
	err      error
	query    TopSnapshotQuery
}

func (f *fakeTopProvider) SnapshotTop(_ context.Context, query TopSnapshotQuery) (TopSnapshot, error) {
	f.query = query
	if f.err != nil {
		return TopSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func TestTopSnapshotRouteReturnsProviderSnapshot(t *testing.T) {
	provider := &fakeTopProvider{snapshot: TopSnapshot{
		Version:       "top/v1",
		Scope:         "local_node",
		GeneratedAt:   time.Unix(1, 0).UTC(),
		WindowSeconds: 10,
		Node:          TopNodeSnapshot{ID: 2, Name: "node-2", Ready: true},
		Verdict:       TopVerdict{Level: "ok", Summary: "ok"},
		ChannelV2: &TopChannelV2{
			ReactorMailboxCapacityMax: 10,
			WorkerQueueCapacityByPool: map[string]int64{"store_append": 8},
			WorkerCapacityByPool:      map[string]int64{"store_append": 6},
		},
		Storage: &TopStorage{CommitQueues: []TopStorageCommitQueue{{
			Store:    "message",
			Capacity: 10,
		}}},
		Sources: TopSources{Collector: TopSourceStatus{Available: true, SampleCount: 11}},
	}}
	s := New(Options{ListenAddr: "127.0.0.1:0", Top: provider})
	req := httptest.NewRequest(http.MethodGet, "/top/v1/snapshot?window=10s&view=all&limit=7", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if provider.query.Window != 10*time.Second || provider.query.View != TopViewAll || provider.query.Limit != 7 {
		t.Fatalf("query = %#v", provider.query)
	}
	for _, want := range []string{
		`"version":"top/v1"`,
		`"scope":"local_node"`,
		`"name":"node-2"`,
		`"reactor_mailbox_capacity_max":10`,
		`"worker_queue_capacity_by_pool":{"store_append":8}`,
		`"worker_capacity_by_pool":{"store_append":6}`,
		`"capacity":10`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestTopSnapshotRouteRejectsInvalidWindow(t *testing.T) {
	s := New(Options{ListenAddr: "127.0.0.1:0", Top: &fakeTopProvider{}})
	req := httptest.NewRequest(http.MethodGet, "/top/v1/snapshot?window=1x", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestTopSnapshotRouteMapsWarmingUpToUnavailable(t *testing.T) {
	s := New(Options{ListenAddr: "127.0.0.1:0", Top: &fakeTopProvider{err: ErrTopWarmingUp}})
	req := httptest.NewRequest(http.MethodGet, "/top/v1/snapshot", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "top collector warming up") || strings.Contains(rec.Body.String(), "internalv2/access/api") {
		t.Fatalf("warming-up response body = %s", rec.Body.String())
	}
}

func TestTopSnapshotRouteWithoutProviderIsNotFound(t *testing.T) {
	s := New(Options{ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest(http.MethodGet, "/top/v1/snapshot", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestTopSnapshotRouteMapsUnexpectedProviderError(t *testing.T) {
	logger := newRecordingAPILogger("internalv2.access.api")
	s := New(Options{ListenAddr: "127.0.0.1:0", Top: &fakeTopProvider{err: errors.New("boom")}, Logger: logger})
	req := httptest.NewRequest(http.MethodGet, "/top/v1/snapshot", nil)
	rec := httptest.NewRecorder()

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	requireAPILogEntry(t, logger, "ERROR", "internalv2.access.api.http", "internalv2.access.api.top_snapshot_failed")
}
