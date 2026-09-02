package manager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerChannelRuntimeMetadataMapsAuthorityFailuresStably(t *testing.T) {
	tests := []struct {
		name string
		err  error
		path string
		want int
	}{
		{name: "invalid cursor", err: metadb.ErrInvalidArgument, path: "/manager/channel-runtime-meta", want: http.StatusBadRequest},
		{name: "exact not found", err: metadb.ErrNotFound, path: "/manager/channel-runtime-meta?exact=true&channel_id=room-a&channel_type=2", want: http.StatusNotFound},
		{name: "slot missing", err: cluster.ErrSlotNotFound, path: "/manager/channel-runtime-meta", want: http.StatusServiceUnavailable},
		{name: "cluster not started", err: cluster.ErrNotStarted, path: "/manager/channel-runtime-meta", want: http.StatusServiceUnavailable},
		{name: "snapshot timeout", err: context.DeadlineExceeded, path: "/manager/channel-runtime-meta", want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("unexpected metadata failure"), path: "/manager/channel-runtime-meta", want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{channelRuntimeMetaErr: test.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestManagerControllerRaftMapsControlPlaneFailuresStably(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, want: http.StatusBadRequest},
		{name: "operator unavailable", err: managementusecase.ErrControllerRaftOperatorUnavailable, want: http.StatusServiceUnavailable},
		{name: "snapshot timeout", err: context.DeadlineExceeded, want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("unexpected raft failure"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{controllerRaftStatusErr: test.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/nodes/2/controller-raft", nil))
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestManagerRaftLogsMapMissingAndUnavailableEvidenceStably(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, want: http.StatusBadRequest},
		{name: "not found", err: metadb.ErrNotFound, want: http.StatusNotFound},
		{name: "reader unavailable", err: managementusecase.ErrLogReaderUnavailable, want: http.StatusServiceUnavailable},
		{name: "snapshot timeout", err: context.DeadlineExceeded, want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("unexpected log failure"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{controllerLogEntriesErr: test.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/controller/logs?node_id=2", nil))
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}

	srv := New(Options{Management: managerNodesStub{slotLogEntriesErr: cluster.ErrSlotNotFound}})
	recorder := httptest.NewRecorder()
	srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/slots/9/logs?node_id=2", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing slot status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestManagerConnectionReadsDistinguishBadIdentityMissingSessionAndUnavailableAuthority(t *testing.T) {
	tests := []struct {
		name string
		err  error
		path string
		want int
	}{
		{name: "invalid list", err: metadb.ErrInvalidArgument, path: "/manager/connections", want: http.StatusBadRequest},
		{name: "missing session", err: metadb.ErrNotFound, path: "/manager/connections/7", want: http.StatusNotFound},
		{name: "reader unavailable", err: managementusecase.ErrConnectionReaderUnavailable, path: "/manager/connections", want: http.StatusServiceUnavailable},
		{name: "snapshot timeout", err: context.DeadlineExceeded, path: "/manager/connections", want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("unexpected connection failure"), path: "/manager/connections", want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{connectionsErr: test.err, connectionDetailErr: test.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestManagerNodeAndSlotInventoriesNeverProjectUnavailableSnapshotsAsEmpty(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		path string
		want int
	}{
		{name: "nodes stopping", err: cluster.ErrStopping, path: "/manager/nodes", want: http.StatusServiceUnavailable},
		{name: "node timeout", err: context.DeadlineExceeded, path: "/manager/nodes/2", want: http.StatusServiceUnavailable},
		{name: "nodes unexpected", err: errors.New("node inventory failed"), path: "/manager/nodes", want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{err: test.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.want || recorder.Code == http.StatusOK {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}

	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "slots stopping", err: cluster.ErrStopping, want: http.StatusServiceUnavailable},
		{name: "slots unexpected", err: errors.New("slot inventory failed"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{slotsErr: test.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/slots", nil))
			if recorder.Code != test.want || recorder.Code == http.StatusOK {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}

	unwired := New(Options{})
	for _, path := range []string{"/manager/nodes", "/manager/slots"} {
		recorder := httptest.NewRecorder()
		unwired.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("unwired path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestManagerSlotRaftCompactionMapsInvalidAndUnavailableRequestsStably(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: metadb.ErrInvalidArgument, want: http.StatusBadRequest},
		{name: "operator unavailable", err: managementusecase.ErrSlotRaftOperatorUnavailable, want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("slot compaction failed"), want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := New(Options{Management: managerNodesStub{slotRaftCompactErr: test.err}})
			recorder := httptest.NewRecorder()
			srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/manager/nodes/2/slots/9/compact", nil))
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}

	for _, path := range []string{"/manager/nodes/0/slots/9/compact", "/manager/nodes/2/slots/0/compact"} {
		srv := New(Options{Management: managerNodesStub{}})
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
