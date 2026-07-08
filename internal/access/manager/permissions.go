package manager

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

type permissionsResponse struct {
	AuthEnabled bool                        `json:"auth_enabled"`
	CurrentUser string                      `json:"current_user"`
	Users       []permissionUserDTO         `json:"users"`
	Resources   []permissionCatalogResource `json:"resources"`
}

type permissionUserDTO struct {
	Username    string               `json:"username"`
	Permissions []permissionGrantDTO `json:"permissions"`
}

type permissionGrantDTO struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

type permissionCatalogResource struct {
	Resource    string   `json:"resource"`
	Actions     []string `json:"actions"`
	Description string   `json:"description"`
}

func (s *Server) handlePermissions(c *gin.Context) {
	currentUser := ""
	if s.auth.enabled() {
		if value, ok := c.Get(managerUsernameContextKey); ok {
			currentUser, _ = value.(string)
		}
	}

	c.JSON(http.StatusOK, permissionsResponse{
		AuthEnabled: s.auth.enabled(),
		CurrentUser: currentUser,
		Users:       s.auth.permissionUsers(),
		Resources:   managerPermissionCatalog(),
	})
}

func (a authState) permissionUsers() []permissionUserDTO {
	if !a.enabled() || len(a.users) == 0 {
		return []permissionUserDTO{}
	}

	usernames := make([]string, 0, len(a.users))
	for username := range a.users {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	out := make([]permissionUserDTO, 0, len(usernames))
	for _, username := range usernames {
		principal := a.users[username]
		grants := make([]permissionGrantDTO, 0, len(principal.grants))
		for _, grant := range principal.grants {
			actions := append([]string(nil), grant.Actions...)
			sort.Strings(actions)
			grants = append(grants, permissionGrantDTO{
				Resource: strings.TrimSpace(grant.Resource),
				Actions:  actions,
			})
		}
		sort.Slice(grants, func(i, j int) bool {
			return grants[i].Resource < grants[j].Resource
		})
		out = append(out, permissionUserDTO{
			Username:    username,
			Permissions: grants,
		})
	}
	return out
}

func managerPermissionCatalog() []permissionCatalogResource {
	return []permissionCatalogResource{
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
}
