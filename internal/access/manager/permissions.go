package manager

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
)

// PermissionSnapshotResponse is the sanitized manager permissions response body.
type PermissionSnapshotResponse struct {
	// AuthEnabled reports whether manager JWT auth and permission checks are enabled.
	AuthEnabled bool `json:"auth_enabled"`
	// CurrentUser is the authenticated manager username when auth is enabled.
	CurrentUser string `json:"current_user"`
	// Users contains static manager users without secrets.
	Users []PermissionUserDTO `json:"users"`
	// Resources contains the built-in manager permission catalog.
	Resources []PermissionResourceDTO `json:"resources"`
}

// PermissionUserDTO describes one static manager user without secrets.
type PermissionUserDTO struct {
	// Username is the static manager login identity.
	Username string `json:"username"`
	// Permissions contains sanitized resource/action grants.
	Permissions []PermissionGrantDTO `json:"permissions"`
}

// PermissionGrantDTO describes one manager resource grant.
type PermissionGrantDTO struct {
	// Resource is the protected manager resource name.
	Resource string `json:"resource"`
	// Actions contains sorted action codes granted on the resource.
	Actions []string `json:"actions"`
}

// PermissionResourceDTO describes one built-in manager permission resource.
type PermissionResourceDTO struct {
	// Resource is the protected manager resource name.
	Resource string `json:"resource"`
	// Actions contains supported action codes for this resource.
	Actions []string `json:"actions"`
	// Description explains the resource scope for operators.
	Description string `json:"description"`
}

func (s *Server) handlePermissions(c *gin.Context) {
	currentUser := ""
	if s.auth.enabled() {
		if value, ok := c.Get(managerUsernameContextKey); ok {
			currentUser, _ = value.(string)
		}
	}
	c.JSON(http.StatusOK, PermissionSnapshotResponse{
		AuthEnabled: s.auth.enabled(),
		CurrentUser: currentUser,
		Users:       s.auth.permissionUsers(),
		Resources:   managerPermissionCatalog(),
	})
}

func (a authState) permissionUsers() []PermissionUserDTO {
	users := make([]PermissionUserDTO, 0, len(a.users))
	if !a.enabled() {
		return users
	}
	for username, principal := range a.users {
		users = append(users, PermissionUserDTO{
			Username:    username,
			Permissions: permissionGrantDTOs(principal.grants),
		})
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users
}

func permissionGrantDTOs(grants []PermissionConfig) []PermissionGrantDTO {
	out := make([]PermissionGrantDTO, 0, len(grants))
	for _, grant := range grants {
		actions := append([]string(nil), grant.Actions...)
		sort.Strings(actions)
		out = append(out, PermissionGrantDTO{Resource: grant.Resource, Actions: actions})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out
}

func managerPermissionCatalog() []PermissionResourceDTO {
	return []PermissionResourceDTO{
		{Resource: "*", Actions: []string{"*"}, Description: "Wildcard access to all manager resources and actions."},
		{Resource: "cluster.overview", Actions: []string{"r"}, Description: "Read dashboard and overview summaries."},
		{Resource: "cluster.node", Actions: []string{"r", "w"}, Description: "Read node inventory and perform node lifecycle actions."},
		{Resource: "cluster.slot", Actions: []string{"r", "w"}, Description: "Read Slot state and perform Slot operations."},
		{Resource: "cluster.controller", Actions: []string{"r", "w"}, Description: "Read or compact Controller Raft logs."},
		{Resource: "cluster.task", Actions: []string{"r"}, Description: "Read reconcile tasks."},
		{Resource: "cluster.connection", Actions: []string{"r"}, Description: "Read connection inventory and details."},
		{Resource: "cluster.network", Actions: []string{"r"}, Description: "Read network diagnostics summaries."},
		{Resource: "cluster.diagnostics", Actions: []string{"r", "w"}, Description: "Read diagnostics and manage temporary message trace sampling rules."},
		{Resource: "cluster.user", Actions: []string{"r", "w"}, Description: "Read users and mutate user or system UID state."},
		{Resource: "cluster.channel", Actions: []string{"r", "w"}, Description: "Read and mutate channel, message, and channel-cluster operations."},
		{Resource: "cluster.plugin", Actions: []string{"r", "w"}, Description: "Read and manage node-local plugins and cluster-wide plugin bindings."},
		{Resource: "cluster.permission", Actions: []string{"r"}, Description: "Read manager authentication and permission configuration snapshots."},
	}
}
