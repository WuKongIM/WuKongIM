package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	applog "github.com/WuKongIM/WuKongIM/internalv2/log"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerApplicationLogEntriesParsesQueryAndReturnsJSON(t *testing.T) {
	var reqSink managementusecase.ApplicationLogEntriesRequest
	parsedAt := time.Date(2026, 6, 17, 12, 3, 4, 123456789, time.UTC)
	srv := New(Options{Management: managerApplicationLogsStub{
		entriesReqSink: &reqSink,
		entriesPage: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 2,
			Source: "warn",
			Cursor: "next-cursor",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:       7,
				Offset:    42,
				Time:      parsedAt,
				Level:     "WARN",
				Module:    "cluster",
				Caller:    "app/server.go:10",
				Message:   "started",
				Fields:    map[string]any{"node": float64(2)},
				Raw:       "raw line",
				Truncated: true,
			}},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/app-logs?node_id=2&source=warn&limit=25&cursor=opaque%2Bcursor&keyword=started&levels=INFO,%20WARN,,", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !sameStrings(reqSink.Levels, []string{"INFO", "WARN"}) ||
		reqSink.NodeID != 2 ||
		reqSink.Source != "warn" ||
		reqSink.Limit != 25 ||
		reqSink.Cursor != "opaque+cursor" ||
		reqSink.Keyword != "started" {
		t.Fatalf("request = %#v, want parsed app log query", reqSink)
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 2,
		"source": "warn",
		"cursor": "next-cursor",
		"rotated": false,
		"items": [{
			"seq": 7,
			"offset": 42,
			"time": "2026-06-17T12:03:04.123456789Z",
			"level": "WARN",
			"module": "cluster",
			"caller": "app/server.go:10",
			"message": "started",
			"fields": {"node": 2},
			"raw": "raw line",
			"truncated": true
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerApplicationLogEntriesDefaultsSource(t *testing.T) {
	var reqSink managementusecase.ApplicationLogEntriesRequest
	srv := New(Options{Management: managerApplicationLogsStub{entriesReqSink: &reqSink}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/app-logs?node_id=3", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if reqSink.Source != "app" {
		t.Fatalf("source = %q, want app", reqSink.Source)
	}
}

func TestManagerApplicationLogSourcesReturnsJSON(t *testing.T) {
	modifiedAt := time.Date(2026, 6, 17, 13, 0, 0, 0, time.UTC)
	srv := New(Options{Management: managerApplicationLogsStub{
		sourcesPage: managementusecase.ApplicationLogSourcesResponse{
			NodeID: 4,
			Sources: []managementusecase.ApplicationLogSource{{
				Name:       "app",
				File:       "app.log",
				Available:  true,
				SizeBytes:  128,
				ModifiedAt: modifiedAt,
			}, {
				Name: "warn",
				File: "warn.log",
			}},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/app-logs/sources?node_id=4", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"node_id": 4,
		"sources": [{
			"name": "app",
			"file": "app.log",
			"available": true,
			"size_bytes": 128,
			"modified_at": "2026-06-17T13:00:00Z"
		}, {
			"name": "warn",
			"file": "warn.log",
			"available": false,
			"size_bytes": 0
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerApplicationLogStreamWritesLineEvent(t *testing.T) {
	srv := New(Options{Management: managerApplicationLogsStub{
		entriesPage: managementusecase.ApplicationLogEntriesResponse{
			NodeID: 2,
			Source: "app",
			Cursor: "cursor-2",
			Items: []managementusecase.ApplicationLogEntry{{
				Seq:     1,
				Offset:  0,
				Level:   "INFO",
				Message: "ready",
				Raw:     "ready",
			}},
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/app-logs/stream?node_id=2", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/x-ndjson") {
		t.Fatalf("content-type = %q, want application/x-ndjson", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
	if !jsonEqual(strings.TrimSpace(rec.Body.String()), `{
		"type": "line",
		"cursor": "cursor-2",
		"item": {
			"seq": 1,
			"offset": 0,
			"level": "INFO",
			"module": "",
			"caller": "",
			"message": "ready",
			"fields": null,
			"raw": "ready",
			"truncated": false
		}
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerApplicationLogRoutesRequireLogPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "node-reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}, {
			Username: "log-reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.log",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerApplicationLogsStub{},
	})

	denied := httptest.NewRecorder()
	deniedReq := httptest.NewRequest(http.MethodGet, "/manager/app-logs/sources?node_id=1", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "node-reader"))
	srv.Engine().ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	allowed := httptest.NewRecorder()
	allowedReq := httptest.NewRequest(http.MethodGet, "/manager/app-logs/sources?node_id=1", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "log-reader"))
	srv.Engine().ServeHTTP(allowed, allowedReq)
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d, want %d; body=%s", allowed.Code, http.StatusOK, allowed.Body.String())
	}
}

func TestManagerApplicationLogRoutesMapErrors(t *testing.T) {
	tests := []struct {
		name   string
		target string
		mgmt   Management
		status int
		body   string
	}{
		{
			name:   "bad node",
			target: "/manager/app-logs?node_id=0",
			mgmt:   managerApplicationLogsStub{},
			status: http.StatusBadRequest,
			body:   `{"error":"bad_request","message":"invalid node_id"}`,
		},
		{
			name:   "bad limit",
			target: "/manager/app-logs?node_id=1&limit=-1",
			mgmt:   managerApplicationLogsStub{},
			status: http.StatusBadRequest,
			body:   `{"error":"bad_request","message":"invalid limit"}`,
		},
		{
			name:   "not found",
			target: "/manager/app-logs?node_id=1",
			mgmt:   managerApplicationLogsStub{entriesErr: metadb.ErrNotFound},
			status: http.StatusNotFound,
			body:   `{"error":"not_found","message":"application log not found"}`,
		},
		{
			name:   "reader unavailable",
			target: "/manager/app-logs?node_id=1",
			mgmt:   managerApplicationLogsStub{entriesErr: managementusecase.ErrApplicationLogReaderUnavailable},
			status: http.StatusServiceUnavailable,
			body:   `{"error":"service_unavailable","message":"application log reader unavailable"}`,
		},
		{
			name:   "management not configured",
			target: "/manager/app-logs?node_id=1",
			mgmt:   nil,
			status: http.StatusServiceUnavailable,
			body:   `{"error":"service_unavailable","message":"management not configured"}`,
		},
		{
			name:   "invalid source",
			target: "/manager/app-logs?node_id=1&source=missing",
			mgmt:   managerApplicationLogsStub{entriesErr: applog.ErrAppLogInvalidSource},
			status: http.StatusBadRequest,
			body:   `{"error":"bad_request","message":"invalid application log source"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: tt.mgmt})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)

			srv.Engine().ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}

type managerApplicationLogsStub struct {
	Management

	sourcesPage    managementusecase.ApplicationLogSourcesResponse
	entriesPage    managementusecase.ApplicationLogEntriesResponse
	sourcesReqSink *managementusecase.ApplicationLogSourcesRequest
	entriesReqSink *managementusecase.ApplicationLogEntriesRequest
	sourcesErr     error
	entriesErr     error
}

func (s managerApplicationLogsStub) ApplicationLogSources(_ context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	if s.sourcesReqSink != nil {
		*s.sourcesReqSink = req
	}
	return s.sourcesPage, s.sourcesErr
}

func (s managerApplicationLogsStub) ApplicationLogEntries(_ context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	if s.entriesReqSink != nil {
		*s.entriesReqSink = req
	}
	return s.entriesPage, s.entriesErr
}

func (s managerNodesStub) ApplicationLogSources(context.Context, managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	return managementusecase.ApplicationLogSourcesResponse{}, nil
}

func (s managerNodesStub) ApplicationLogEntries(context.Context, managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	return managementusecase.ApplicationLogEntriesResponse{}, nil
}
