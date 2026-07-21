package manager

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// ManagerPermissionsResponse is the read-only manager permission catalog response.
type ManagerPermissionsResponse struct {
	// AuthEnabled reports whether manager route authentication is enabled.
	AuthEnabled bool `json:"auth_enabled"`
	// CurrentUser is the authenticated manager username for this request.
	CurrentUser string `json:"current_user"`
	// Users lists configured static manager users without secrets.
	Users []ManagerPermissionUser `json:"users"`
	// Resources lists built-in manager permission resources and supported actions.
	Resources []ManagerPermissionResource `json:"resources"`
}

// ManagerPermissionUser describes one static manager user and its grants.
type ManagerPermissionUser struct {
	// Username is the configured manager login identity.
	Username string `json:"username"`
	// Permissions lists the grants assigned to this user.
	Permissions []ManagerPermissionGrant `json:"permissions"`
}

// ManagerPermissionGrant binds one manager resource to allowed actions.
type ManagerPermissionGrant struct {
	// Resource is the protected manager resource name.
	Resource string `json:"resource"`
	// Actions contains supported action codes for this grant.
	Actions []string `json:"actions"`
}

// ManagerPermissionResource describes one built-in manager permission resource.
type ManagerPermissionResource struct {
	// Resource is the protected manager resource name.
	Resource string `json:"resource"`
	// Actions contains supported action codes for this resource.
	Actions []string `json:"actions"`
	// Description explains what the resource grants.
	Description string `json:"description"`
}

var builtinManagerPermissionResources = []ManagerPermissionResource{
	{
		Resource:    "cluster.node",
		Actions:     []string{"r", "w"},
		Description: "Read node inventory and perform node lifecycle actions.",
	},
	{
		Resource:    "cluster.slot",
		Actions:     []string{"r", "w"},
		Description: "Read Slot state and perform Slot operations.",
	},
	{
		Resource:    "cluster.controller",
		Actions:     []string{"r", "w"},
		Description: "Read Controller state and perform Controller operations.",
	},
	{
		Resource:    "cluster.backup",
		Actions:     []string{"r", "w"},
		Description: "Read cluster backup evidence and control backup jobs or restore-point holds.",
	},
	{
		Resource:    "cluster.restore.activation",
		Actions:     []string{"w"},
		Description: "Activate a verified successor only with a separately reviewed explicit grant; wildcard grants do not apply.",
	},
	{
		Resource:    "cluster.diagnostics",
		Actions:     []string{"r", "w"},
		Description: "Read diagnostics data and manage tracking rules.",
	},
	{
		Resource:    "cluster.log",
		Actions:     []string{"r"},
		Description: "Read ordinary application log sources and entries.",
	},
	{
		Resource:    "cluster.db",
		Actions:     []string{"r"},
		Description: "Read node-local DB Inspect table metadata and query results.",
	},
	{
		Resource:    "cluster.channel",
		Actions:     []string{"r", "w"},
		Description: "Read and mutate channel, message, and channel migration state.",
	},
	{
		Resource:    "cluster.connection",
		Actions:     []string{"r"},
		Description: "Read connection inventory and connection details.",
	},
	{
		Resource:    "cluster.webhook",
		Actions:     []string{"r"},
		Description: "Read webhook startup configuration snapshots.",
	},
	{
		Resource:    "cluster.plugin",
		Actions:     []string{"r", "w"},
		Description: "Read and mutate node-local plugin state and UID plugin bindings.",
	},
	{
		Resource:    "cluster.user",
		Actions:     []string{"r", "w"},
		Description: "Read users and mutate user or system UID state.",
	},
	{
		Resource:    "cluster.permission",
		Actions:     []string{"r"},
		Description: "Read manager authentication and permission configuration snapshots.",
	},
	{
		Resource:    "*",
		Actions:     []string{"*"},
		Description: "Wildcard access to all manager resources and actions.",
	},
}

func (s *Server) handlePermissions(c *gin.Context) {
	c.JSON(http.StatusOK, ManagerPermissionsResponse{
		AuthEnabled: s.auth.enabled(),
		CurrentUser: currentManagerUsername(c),
		Users:       s.managerPermissionUsers(),
		Resources:   managerPermissionResources(),
	})
}

func currentManagerUsername(c *gin.Context) string {
	if c == nil {
		return ""
	}
	username, ok := c.Get(managerUsernameContextKey)
	if !ok {
		return ""
	}
	value, ok := username.(string)
	if !ok {
		return ""
	}
	return value
}

func (s *Server) managerPermissionUsers() []ManagerPermissionUser {
	if s == nil || !s.auth.enabled() || len(s.auth.users) == 0 {
		return []ManagerPermissionUser{}
	}

	usernames := make([]string, 0, len(s.auth.users))
	for username := range s.auth.users {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	out := make([]ManagerPermissionUser, 0, len(usernames))
	for _, username := range usernames {
		out = append(out, ManagerPermissionUser{
			Username:    username,
			Permissions: managerPermissionGrants(s.auth.permissionsFor(username)),
		})
	}
	return out
}

func managerPermissionGrants(grants []PermissionConfig) []ManagerPermissionGrant {
	out := make([]ManagerPermissionGrant, 0, len(grants))
	for _, grant := range grants {
		actions := append([]string(nil), grant.Actions...)
		sort.Strings(actions)
		out = append(out, ManagerPermissionGrant{
			Resource: strings.TrimSpace(grant.Resource),
			Actions:  actions,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Resource < out[j].Resource
	})
	return out
}

func managerPermissionResources() []ManagerPermissionResource {
	out := make([]ManagerPermissionResource, 0, len(builtinManagerPermissionResources))
	for _, resource := range builtinManagerPermissionResources {
		out = append(out, ManagerPermissionResource{
			Resource:    resource.Resource,
			Actions:     append([]string(nil), resource.Actions...),
			Description: resource.Description,
		})
	}
	return out
}
