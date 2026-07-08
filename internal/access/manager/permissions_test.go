package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerPermissionsReturnsConfiguredUsersAndCatalog(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.permission",
				Actions:  []string{"r"},
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
	var body ManagerPermissionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !body.AuthEnabled {
		t.Fatalf("auth_enabled = false, want true")
	}
	if body.CurrentUser != "admin" {
		t.Fatalf("current_user = %q, want %q", body.CurrentUser, "admin")
	}
	if len(body.Users) != 1 || body.Users[0].Username != "admin" {
		t.Fatalf("users = %#v, want one admin row", body.Users)
	}
	if len(body.Users[0].Permissions) != 1 ||
		body.Users[0].Permissions[0].Resource != "cluster.permission" ||
		len(body.Users[0].Permissions[0].Actions) != 1 ||
		body.Users[0].Permissions[0].Actions[0] != "r" {
		t.Fatalf("admin permissions = %#v, want cluster.permission:r", body.Users[0].Permissions)
	}
	if !permissionCatalogContains(body.Resources, "cluster.webhook", "r") {
		t.Fatalf("resources = %#v, want cluster.webhook:r", body.Resources)
	}
	if permissionCatalogContains(body.Resources, "cluster.webhook", "w") {
		t.Fatalf("resources = %#v, did not expect cluster.webhook:w", body.Resources)
	}
	if !permissionCatalogContains(body.Resources, "*", "*") {
		t.Fatalf("resources = %#v, want wildcard entry", body.Resources)
	}

	catalog := managerPermissionResources()
	catalog[0].Actions[0] = "mutated"
	catalog[0].Description = "mutated"
	freshCatalog := managerPermissionResources()
	if freshCatalog[0].Actions[0] == "mutated" || freshCatalog[0].Description == "mutated" {
		t.Fatalf("managerPermissionResources() returned mutable package-level slices: %#v", freshCatalog[0])
	}
}

func TestManagerPermissionsRequiresPermissionRead(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/permissions", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func permissionCatalogContains(resources []ManagerPermissionResource, resource, action string) bool {
	for _, item := range resources {
		if item.Resource != resource {
			continue
		}
		for _, candidate := range item.Actions {
			if candidate == action {
				return true
			}
		}
	}
	return false
}
