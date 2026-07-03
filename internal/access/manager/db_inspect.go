package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	dbinspect "github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

type dbInspectQueryRequestDTO struct {
	NodeID uint64 `json:"node_id"`
	Query  string `json:"query"`
}

type dbInspectQueryResponseDTO struct {
	NodeID      uint64                    `json:"node_id"`
	GeneratedAt time.Time                 `json:"generated_at"`
	Rows        []map[string]any          `json:"rows"`
	Stats       dbInspectStatsResponseDTO `json:"stats"`
}

type dbInspectStatsResponseDTO struct {
	ScanMode         string   `json:"scan_mode"`
	ScannedHashSlots []uint16 `json:"scanned_hash_slots"`
	ScannedRows      int      `json:"scanned_rows"`
	ReturnedRows     int      `json:"returned_rows"`
	HasMore          bool     `json:"has_more"`
	NextCursor       string   `json:"next_cursor"`
}

func (s *Server) handleDBInspectTables(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseDBInspectNodeID(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid node_id")
		return
	}
	resp, err := s.management.ListDBInspectTables(c.Request.Context(), nodeID)
	if err != nil {
		writeDBInspectError(c, err)
		return
	}
	c.JSON(http.StatusOK, dbInspectQueryDTO(resp))
}

func (s *Server) handleDBInspectTable(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseDBInspectNodeID(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid node_id")
		return
	}
	resp, err := s.management.DescribeDBInspectTable(c.Request.Context(), nodeID, c.Param("domain"), c.Param("table"))
	if err != nil {
		writeDBInspectError(c, err)
		return
	}
	c.JSON(http.StatusOK, dbInspectQueryDTO(resp))
}

func (s *Server) handleDBInspectQuery(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	var body dbInspectQueryRequestDTO
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid db inspect query")
		return
	}
	if strings.TrimSpace(body.Query) == "" {
		jsonError(c, http.StatusBadRequest, "invalid_request", "query is required")
		return
	}
	resp, err := s.management.QueryDBInspect(c.Request.Context(), managementusecase.DBInspectQueryRequest{
		NodeID: body.NodeID,
		Query:  body.Query,
	})
	if err != nil {
		writeDBInspectError(c, err)
		return
	}
	c.JSON(http.StatusOK, dbInspectQueryDTO(resp))
}

func parseDBInspectNodeID(raw string) (uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func dbInspectQueryDTO(resp managementusecase.DBInspectQueryResponse) dbInspectQueryResponseDTO {
	rows := make([]map[string]any, 0, len(resp.Rows))
	for _, row := range resp.Rows {
		next := make(map[string]any, len(row))
		for key, value := range row {
			next[key] = value
		}
		rows = append(rows, next)
	}
	return dbInspectQueryResponseDTO{
		NodeID:      resp.NodeID,
		GeneratedAt: resp.GeneratedAt,
		Rows:        rows,
		Stats: dbInspectStatsResponseDTO{
			ScanMode:         resp.Stats.ScanMode,
			ScannedHashSlots: append([]uint16(nil), resp.Stats.ScannedHashSlots...),
			ScannedRows:      resp.Stats.ScannedRows,
			ReturnedRows:     resp.Stats.ReturnedRows,
			HasMore:          resp.Stats.HasMore,
			NextCursor:       resp.Stats.NextCursor,
		},
	}
}

func writeDBInspectError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, dbinspect.ErrCursorMismatch):
		jsonError(c, http.StatusBadRequest, "invalid_cursor", "invalid cursor")
	case errors.Is(err, dbinspect.ErrInvalidQuery), errors.Is(err, dbinspect.ErrUnsupportedQuery), errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid db inspect query")
	case errors.Is(err, managementusecase.ErrDBInspectUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "db inspect unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", "db inspect query failed")
	}
}
