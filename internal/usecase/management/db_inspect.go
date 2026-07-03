package management

import (
	"context"
	"errors"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrDBInspectUnavailable reports that read-only DB inspection is not wired.
var ErrDBInspectUnavailable = errors.New("management: db inspect unavailable")

// DBInspectReader executes read-only inspect queries against one node-local DB.
type DBInspectReader interface {
	// QueryDBInspect runs one bounded read-only inspect query.
	QueryDBInspect(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error)
}

// RemoteDBInspectReader executes read-only inspect queries on peer nodes.
type RemoteDBInspectReader interface {
	// NodeDBInspectQuery runs one bounded read-only inspect query on req.NodeID.
	NodeDBInspectQuery(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error)
}

// DBInspectRow is one JSON-friendly inspect result row.
type DBInspectRow map[string]any

// DBInspectQueryRequest configures one manager DB inspect query.
type DBInspectQueryRequest struct {
	// NodeID selects the node-local DB to inspect. Zero means the local node.
	NodeID uint64
	// Query is the read-only inspect SQL statement.
	Query string
}

// DBInspectQueryResponse is one manager DB inspect result.
type DBInspectQueryResponse struct {
	// NodeID is the node that executed the query.
	NodeID uint64
	// GeneratedAt records when the result was produced.
	GeneratedAt time.Time
	// Rows contains dynamic inspect result rows.
	Rows []DBInspectRow
	// Stats summarizes scan work and pagination state.
	Stats DBInspectStats
}

// DBInspectStats summarizes inspect query execution.
type DBInspectStats struct {
	// ScanMode names the scan strategy used by pkg/db/inspect.
	ScanMode string
	// ScannedHashSlots lists metadata hash slots touched by the scan.
	ScannedHashSlots []uint16
	// ScannedRows counts decoded rows considered by the scan.
	ScannedRows int
	// ReturnedRows counts rows returned to the caller.
	ReturnedRows int
	// HasMore reports whether another page is available.
	HasMore bool
	// NextCursor resumes the next page when HasMore is true.
	NextCursor string
}

// ListDBInspectTables returns inspectable DB tables for one node.
func (a *App) ListDBInspectTables(ctx context.Context, nodeID uint64) (DBInspectQueryResponse, error) {
	return a.QueryDBInspect(ctx, DBInspectQueryRequest{NodeID: nodeID, Query: "show tables"})
}

// DescribeDBInspectTable returns inspectable column metadata for one table.
func (a *App) DescribeDBInspectTable(ctx context.Context, nodeID uint64, domain, table string) (DBInspectQueryResponse, error) {
	domain = strings.TrimSpace(domain)
	table = strings.TrimSpace(table)
	if !validDBInspectIdentifier(domain) || !validDBInspectIdentifier(table) {
		return DBInspectQueryResponse{}, metadb.ErrInvalidArgument
	}
	return a.QueryDBInspect(ctx, DBInspectQueryRequest{NodeID: nodeID, Query: "describe " + domain + "." + table})
}

// QueryDBInspect runs one read-only DB inspect query on the selected node.
func (a *App) QueryDBInspect(ctx context.Context, req DBInspectQueryRequest) (DBInspectQueryResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return DBInspectQueryResponse{}, metadb.ErrInvalidArgument
	}
	nodeID, local, err := a.resolveDBInspectNode(ctx, req.NodeID)
	if err != nil {
		return DBInspectQueryResponse{}, err
	}
	next := DBInspectQueryRequest{NodeID: nodeID, Query: query}
	if local {
		if a == nil || a.dbInspect == nil {
			return DBInspectQueryResponse{}, ErrDBInspectUnavailable
		}
		return a.dbInspect.QueryDBInspect(ctx, next)
	}
	if a == nil || a.remoteDBInspect == nil {
		return DBInspectQueryResponse{}, ErrDBInspectUnavailable
	}
	return a.remoteDBInspect.NodeDBInspectQuery(ctx, next)
}

func (a *App) resolveDBInspectNode(ctx context.Context, nodeID uint64) (uint64, bool, error) {
	if a == nil || a.cluster == nil {
		if nodeID == 0 {
			return 0, true, nil
		}
		return 0, false, metadb.ErrInvalidArgument
	}
	localID := a.cluster.NodeID()
	if nodeID == 0 || nodeID == localID {
		return localID, true, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return nodeID, false, nil
		}
	}
	return 0, false, metadb.ErrInvalidArgument
}

func validDBInspectIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return false
	}
	return true
}
