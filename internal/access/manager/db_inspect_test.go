package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestDBInspectRoutesRequirePermission(t *testing.T) {
	allowed := New(Options{
		ListenAddr: "127.0.0.1:0",
		Auth: AuthConfig{
			On:        true,
			JWTSecret: "secret",
			JWTIssuer: "manager-test",
			JWTExpire: time.Hour,
			Users: []UserConfig{{
				Username:    "admin",
				Password:    "pw",
				Permissions: []PermissionConfig{{Resource: "cluster.db", Actions: []string{"r"}}},
			}},
		},
		Management: &fakeDBInspectManagement{resp: managementusecase.DBInspectQueryResponse{NodeID: 1}},
	})
	token := mustIssueTestToken(t, allowed, "admin")
	req := httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":"show tables"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	allowed.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	denied := New(Options{
		ListenAddr: "127.0.0.1:0",
		Auth: AuthConfig{
			On:        true,
			JWTSecret: "secret",
			JWTIssuer: "manager-test",
			JWTExpire: time.Hour,
			Users: []UserConfig{{
				Username:    "admin",
				Password:    "pw",
				Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}},
			}},
		},
		Management: &fakeDBInspectManagement{},
	})
	token = mustIssueTestToken(t, denied, "admin")
	req = httptest.NewRequest(http.MethodGet, "/manager/db/inspect/tables", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	denied.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want 403", rec.Code)
	}
}

func TestDBInspectQuerySuccess(t *testing.T) {
	mgmt := &fakeDBInspectManagement{
		resp: managementusecase.DBInspectQueryResponse{
			NodeID:      2,
			GeneratedAt: time.Unix(100, 0).UTC(),
			Rows: []managementusecase.DBInspectRow{
				{"table": "meta.user"},
			},
			Stats: managementusecase.DBInspectStats{ScanMode: "local-bounded", ReturnedRows: 1},
		},
	}
	srv := New(Options{ListenAddr: "127.0.0.1:0", Management: mgmt})
	req := httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"node_id":2,"query":"show tables"}`))
	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var body dbInspectQueryResponseDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.NodeID != 2 || body.Rows[0]["table"] != "meta.user" || body.Stats.ScanMode != "local-bounded" {
		t.Fatalf("body = %#v, want DB inspect response", body)
	}
	if len(mgmt.queryCalls) != 1 || mgmt.queryCalls[0].NodeID != 2 || mgmt.queryCalls[0].Query != "show tables" {
		t.Fatalf("query calls = %#v, want forwarded request", mgmt.queryCalls)
	}
}

func TestDBInspectTablesAndDescribe(t *testing.T) {
	mgmt := &fakeDBInspectManagement{
		resp: managementusecase.DBInspectQueryResponse{NodeID: 1, Rows: []managementusecase.DBInspectRow{{"column": "uid"}}},
	}
	srv := New(Options{ListenAddr: "127.0.0.1:0", Management: mgmt})

	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manager/db/inspect/tables?node_id=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("tables status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if len(mgmt.tableCalls) != 1 || mgmt.tableCalls[0] != 1 {
		t.Fatalf("table calls = %#v, want node 1", mgmt.tableCalls)
	}

	rec = httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manager/db/inspect/tables/meta/user?node_id=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("describe status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if len(mgmt.describeCalls) != 1 || mgmt.describeCalls[0] != "1:meta.user" {
		t.Fatalf("describe calls = %#v, want node 1 meta.user", mgmt.describeCalls)
	}
}

func TestDBInspectHTTPValidationAndUnavailable(t *testing.T) {
	srv := New(Options{ListenAddr: "127.0.0.1:0", Management: &fakeDBInspectManagement{err: managementusecase.ErrDBInspectUnavailable}})

	rec := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":""}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty query status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":"show tables"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, want 503", rec.Code)
	}
}

func TestDBInspectMapsInvalidAndCursorErrorsToBadRequest(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "invalid argument", err: metadb.ErrInvalidArgument},
		{name: "invalid query", err: dbinspect.ErrInvalidQuery},
		{name: "unsupported query", err: dbinspect.ErrUnsupportedQuery},
		{name: "cursor mismatch", err: dbinspect.ErrCursorMismatch},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{ListenAddr: "127.0.0.1:0", Management: &fakeDBInspectManagement{err: tt.err}})
			rec := httptest.NewRecorder()
			srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/manager/db/inspect/query", strings.NewReader(`{"query":"show tables"}`)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}

type fakeDBInspectManagement struct {
	managerNodesStub
	resp          managementusecase.DBInspectQueryResponse
	err           error
	queryCalls    []managementusecase.DBInspectQueryRequest
	tableCalls    []uint64
	describeCalls []string
}

func (f *fakeDBInspectManagement) QueryDBInspect(_ context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	f.queryCalls = append(f.queryCalls, req)
	return f.resp, f.err
}

func (f *fakeDBInspectManagement) ListDBInspectTables(_ context.Context, nodeID uint64) (managementusecase.DBInspectQueryResponse, error) {
	f.tableCalls = append(f.tableCalls, nodeID)
	return f.resp, f.err
}

func (f *fakeDBInspectManagement) DescribeDBInspectTable(_ context.Context, nodeID uint64, domain, table string) (managementusecase.DBInspectQueryResponse, error) {
	f.describeCalls = append(f.describeCalls, fmt.Sprintf("%d:%s.%s", nodeID, domain, table))
	return f.resp, f.err
}

func (s managerNodesStub) QueryDBInspect(context.Context, managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	return managementusecase.DBInspectQueryResponse{}, errors.New("unexpected db inspect query")
}

func (s managerNodesStub) ListDBInspectTables(context.Context, uint64) (managementusecase.DBInspectQueryResponse, error) {
	return managementusecase.DBInspectQueryResponse{}, errors.New("unexpected db inspect table list")
}

func (s managerNodesStub) DescribeDBInspectTable(context.Context, uint64, string, string) (managementusecase.DBInspectQueryResponse, error) {
	return managementusecase.DBInspectQueryResponse{}, errors.New("unexpected db inspect table describe")
}
