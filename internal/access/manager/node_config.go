package manager

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// NodeConfigResponse is the manager selected-node config response body.
type NodeConfigResponse struct {
	// GeneratedAt is the timestamp when the selected-node config snapshot was built.
	GeneratedAt time.Time `json:"generated_at"`
	// NodeID is the selected cluster node identifier.
	NodeID uint64 `json:"node_id"`
	// Source names the source class for the effective config snapshot.
	Source string `json:"source"`
	// RequiresRestart reports whether these startup settings require restart to change.
	RequiresRestart bool `json:"requires_restart"`
	// Groups contains stable allowlisted config sections.
	Groups []NodeConfigGroupResponse `json:"groups"`
}

// NodeConfigGroupResponse is one selected-node config section.
type NodeConfigGroupResponse struct {
	// ID is the stable group identifier used by the manager UI.
	ID string `json:"id"`
	// Title is the operator-facing group title.
	Title string `json:"title"`
	// Items contains the allowlisted config values in this group.
	Items []NodeConfigItemResponse `json:"items"`
}

// NodeConfigItemResponse is one allowlisted selected-node config value.
type NodeConfigItemResponse struct {
	// Key is the canonical WK_* config key.
	Key string `json:"key"`
	// Label is the concise operator-facing item label.
	Label string `json:"label"`
	// Value is the already-formatted effective value.
	Value string `json:"value"`
	// Sensitive reports whether the underlying setting is sensitive.
	Sensitive bool `json:"sensitive"`
	// Redacted reports whether Value is a fixed redaction token.
	Redacted bool `json:"redacted"`
}

func (s *Server) handleNodeConfig(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseManagerNodeConfigNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	snapshot, err := s.management.NodeConfigSnapshot(c.Request.Context(), nodeID)
	if err != nil {
		writeNodeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeConfigResponseFromUsecase(snapshot))
}

func parseManagerNodeConfigNodeID(raw string) (uint64, error) {
	nodeID, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || nodeID == 0 {
		return 0, metadb.ErrInvalidArgument
	}
	return nodeID, nil
}

func writeNodeConfigError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node config request")
	case errors.Is(err, metadb.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "node not found")
	case errors.Is(err, managementusecase.ErrNodeConfigUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "node config unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func nodeConfigResponseFromUsecase(snapshot managementusecase.NodeConfigSnapshot) NodeConfigResponse {
	groups := make([]NodeConfigGroupResponse, 0, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		items := make([]NodeConfigItemResponse, 0, len(group.Items))
		for _, item := range group.Items {
			items = append(items, NodeConfigItemResponse{
				Key:       item.Key,
				Label:     item.Label,
				Value:     item.Value,
				Sensitive: item.Sensitive,
				Redacted:  item.Redacted,
			})
		}
		groups = append(groups, NodeConfigGroupResponse{
			ID:    group.ID,
			Title: group.Title,
			Items: items,
		})
	}
	return NodeConfigResponse{
		GeneratedAt:     snapshot.GeneratedAt,
		NodeID:          snapshot.NodeID,
		Source:          snapshot.Source,
		RequiresRestart: snapshot.RequiresRestart,
		Groups:          groups,
	}
}
