package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagerPermissionsReturnsSanitizedSnapshot(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{
			{
				Username: "viewer",
				Password: "viewer-password",
				Permissions: []PermissionConfig{{
					Resource: "cluster.node",
					Actions:  []string{"r"},
				}},
			},
			{
				Username: "admin",
				Password: "admin-password",
				Permissions: []PermissionConfig{
					{
						Resource: "cluster.permission",
						Actions:  []string{"r"},
					},
					{
						Resource: "cluster.node",
						Actions:  []string{"w", "r"},
					},
				},
			},
		}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	rawBody := rec.Body.String()
	for _, secret := range []string{"admin-password", "viewer-password", "test-secret"} {
		if strings.Contains(rawBody, secret) {
			t.Fatalf("permissions body leaked secret %q: %s", secret, rawBody)
		}
	}

	var body permissionsResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !body.AuthEnabled || body.CurrentUser != "admin" {
		t.Fatalf("summary = %+v, want auth enabled for admin", body)
	}
	if len(body.Users) != 2 || body.Users[0].Username != "admin" || body.Users[1].Username != "viewer" {
		t.Fatalf("users = %#v, want sorted sanitized users", body.Users)
	}
	if got := body.Users[0].Permissions[0]; got.Resource != "cluster.node" || strings.Join(got.Actions, ",") != "r,w" {
		t.Fatalf("admin first grant = %#v, want sorted cluster.node r,w", got)
	}
	if got := body.Users[0].Permissions[1]; got.Resource != "cluster.permission" || strings.Join(got.Actions, ",") != "r" {
		t.Fatalf("admin second grant = %#v, want cluster.permission r", got)
	}

	requirePermissionCatalogEntry(t, body.Resources, "cluster.permission", []string{"r"})
	requirePermissionCatalogEntry(t, body.Resources, "*", []string{"*"})
}

func TestManagerPermissionsRequiresPermissionRead(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "node-reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "node-reader"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestManagerPermissionsAllowsWildcardPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestManagerPermissionsAuthDisabledReturnsEmptyUsers(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body permissionsResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if body.AuthEnabled || body.CurrentUser != "" || len(body.Users) != 0 {
		t.Fatalf("body = %+v, want auth disabled with empty users", body)
	}
	requirePermissionCatalogEntry(t, body.Resources, "*", []string{"*"})
}

type permissionsResponseBody struct {
	AuthEnabled bool                      `json:"auth_enabled"`
	CurrentUser string                    `json:"current_user"`
	Users       []permissionsUserBody     `json:"users"`
	Resources   []permissionsResourceBody `json:"resources"`
}

type permissionsUserBody struct {
	Username    string           `json:"username"`
	Permissions []permissionBody `json:"permissions"`
}

type permissionsResourceBody struct {
	Resource    string   `json:"resource"`
	Actions     []string `json:"actions"`
	Description string   `json:"description"`
}

func requirePermissionCatalogEntry(t *testing.T, resources []permissionsResourceBody, resource string, actions []string) {
	t.Helper()
	for _, item := range resources {
		if item.Resource != resource {
			continue
		}
		if strings.Join(item.Actions, ",") != strings.Join(actions, ",") {
			t.Fatalf("catalog %q actions = %#v, want %#v", resource, item.Actions, actions)
		}
		if strings.TrimSpace(item.Description) == "" {
			t.Fatalf("catalog %q has empty description", resource)
		}
		return
	}
	t.Fatalf("catalog missing %q in %#v", resource, resources)
}
