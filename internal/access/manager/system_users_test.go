package manager

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerSystemUsersListReturnsRows(t *testing.T) {
	srv := New(Options{Management: managementStub{systemUsers: managementusecase.ListSystemUsersResponse{
		Items: []managementusecase.SystemUser{{UID: "sys-a"}, {UID: "sys-b"}},
		Total: 2,
	}}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"items":[{"uid":"sys-a"},{"uid":"sys-b"}],"total":2}`, rec.Body.String())
}

func TestManagerSystemUsersAddPostsUIDs(t *testing.T) {
	var captured managementusecase.MutateSystemUsersRequest
	srv := New(Options{Management: managementStub{
		systemUsersAddReqSink: &captured,
		systemUsersMutation:   managementusecase.MutateSystemUsersResponse{UIDs: []string{"sys-a", "sys-b"}, Changed: true},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{"uids":["sys-a","sys-b"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"sys-a", "sys-b"}, captured.UIDs)
	require.JSONEq(t, `{"uids":["sys-a","sys-b"],"changed":true}`, rec.Body.String())
}

func TestManagerSystemUsersRemovePostsUIDs(t *testing.T) {
	var captured managementusecase.MutateSystemUsersRequest
	srv := New(Options{Management: managementStub{
		systemUsersRemoveReqSink: &captured,
		systemUsersMutation:      managementusecase.MutateSystemUsersResponse{UIDs: []string{"sys-a"}, Changed: true},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/system-users/remove", bytes.NewBufferString(`{"uids":["sys-a"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"sys-a"}, captured.UIDs)
	require.JSONEq(t, `{"uids":["sys-a"],"changed":true}`, rec.Body.String())
}

func TestManagerSystemUsersRejectsInvalidJSON(t *testing.T) {
	srv := New(Options{Management: managementStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid system user request"}`, rec.Body.String())
}

func TestManagerSystemUsersMapsInvalidArgument(t *testing.T) {
	srv := New(Options{Management: managementStub{systemUsersMutationErr: metadb.ErrInvalidArgument}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{"uids":[]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid system user request"}`, rec.Body.String())
}

func TestManagerSystemUsersRequiresReadPermission(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig([]UserConfig{{Username: "viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}}}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerSystemUsersRequiresWritePermission(t *testing.T) {
	srv := New(Options{
		Auth:       testAuthConfig([]UserConfig{{Username: "viewer", Password: "secret", Permissions: []PermissionConfig{{Resource: "cluster.user", Actions: []string{"r"}}}}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{"uids":["sys-a"]}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerSystemUsersReturnsUnavailableWithoutManagement(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
