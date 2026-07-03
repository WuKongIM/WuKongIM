package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagerPermissionsReturnsSanitizedSnapshot(t *testing.T) {
	srv := New(Options{
		Auth: AuthConfig{
			On:        true,
			JWTSecret: "jwt-secret-that-must-not-leak",
			JWTIssuer: "test-manager",
			Users: []UserConfig{
				{
					Username: "viewer",
					Password: "viewer-password-that-must-not-leak",
					Permissions: []PermissionConfig{
						{Resource: "cluster.node", Actions: []string{"r"}},
					},
				},
				{
					Username: "admin",
					Password: "admin-password-that-must-not-leak",
					Permissions: []PermissionConfig{
						{Resource: "cluster.permission", Actions: []string{"r"}},
						{Resource: "cluster.node", Actions: []string{"w", "r"}},
					},
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "admin-password-that-must-not-leak")
	require.NotContains(t, rec.Body.String(), "viewer-password-that-must-not-leak")
	require.NotContains(t, rec.Body.String(), "jwt-secret-that-must-not-leak")
	require.JSONEq(t, `{
		"auth_enabled": true,
		"current_user": "admin",
		"users": [
			{
				"username": "admin",
				"permissions": [
					{"resource":"cluster.node","actions":["r","w"]},
					{"resource":"cluster.permission","actions":["r"]}
				]
			},
			{
				"username": "viewer",
				"permissions": [
					{"resource":"cluster.node","actions":["r"]}
				]
			}
		],
		"resources": [
			{"resource":"*","actions":["*"],"description":"Wildcard access to all manager resources and actions."},
			{"resource":"cluster.overview","actions":["r"],"description":"Read dashboard and overview summaries."},
			{"resource":"cluster.node","actions":["r","w"],"description":"Read node inventory and perform node lifecycle actions."},
			{"resource":"cluster.slot","actions":["r","w"],"description":"Read Slot state and perform Slot operations."},
			{"resource":"cluster.controller","actions":["r","w"],"description":"Read or compact Controller Raft logs."},
			{"resource":"cluster.task","actions":["r"],"description":"Read reconcile tasks."},
			{"resource":"cluster.connection","actions":["r"],"description":"Read connection inventory and details."},
			{"resource":"cluster.network","actions":["r"],"description":"Read network diagnostics summaries."},
			{"resource":"cluster.diagnostics","actions":["r","w"],"description":"Read diagnostics and manage temporary message trace sampling rules."},
			{"resource":"cluster.user","actions":["r","w"],"description":"Read users and mutate user or system UID state."},
			{"resource":"cluster.channel","actions":["r","w"],"description":"Read and mutate channel, message, and channel-cluster operations."},
			{"resource":"cluster.plugin","actions":["r","w"],"description":"Read and manage node-local plugins and cluster-wide plugin bindings."},
			{"resource":"cluster.permission","actions":["r"],"description":"Read manager authentication and permission configuration snapshots."}
		]
	}`, rec.Body.String())
}

func TestManagerPermissionsRequiresPermission(t *testing.T) {
	srv := New(Options{Auth: testAuthConfig([]UserConfig{{
		Username:    "viewer",
		Password:    "secret",
		Permissions: []PermissionConfig{{Resource: "cluster.node", Actions: []string{"r"}}},
	}})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestManagerPermissionsAllowsWildcardPermission(t *testing.T) {
	srv := New(Options{Auth: testAuthConfig([]UserConfig{{
		Username:    "root",
		Password:    "secret",
		Permissions: []PermissionConfig{{Resource: "*", Actions: []string{"*"}}},
	}})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "root"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestManagerPermissionsReturnsDisabledSnapshotWithoutAuth(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body PermissionSnapshotResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.False(t, body.AuthEnabled)
	require.Empty(t, body.CurrentUser)
	require.Empty(t, body.Users)
	require.NotEmpty(t, body.Resources)
}
