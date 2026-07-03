package app

import (
	"context"
	"path/filepath"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

type dbInspectReaderOptions struct {
	// NodeID is the local node ID reported on local inspect responses.
	NodeID uint64
	// DataDir is the node root data directory that contains slotmeta and messages.
	DataDir string
	// HashSlotCount bounds metadata scans across hash slots.
	HashSlotCount uint16
	// Now returns the timestamp attached to inspect responses.
	Now func() time.Time
}

// dbInspectReader adapts pkg/db/inspect to the manager management usecase.
type dbInspectReader struct {
	// nodeID is the local node ID reported when requests omit a node.
	nodeID uint64
	// dataDir is the node root data directory that contains inspectable stores.
	dataDir string
	// hashSlotCount bounds metadata scans across hash slots.
	hashSlotCount uint16
	// now returns the timestamp attached to inspect responses.
	now func() time.Time
}

func newDBInspectReader(opts dbInspectReaderOptions) *dbInspectReader {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	hashSlotCount := opts.HashSlotCount
	if hashSlotCount == 0 {
		hashSlotCount = 16
	}
	return &dbInspectReader{
		nodeID:        opts.NodeID,
		dataDir:       opts.DataDir,
		hashSlotCount: hashSlotCount,
		now:           now,
	}
}

// QueryDBInspect runs one read-only inspect query against local node storage.
func (r *dbInspectReader) QueryDBInspect(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	if r == nil || r.dataDir == "" {
		return managementusecase.DBInspectQueryResponse{}, managementusecase.ErrDBInspectUnavailable
	}
	store, err := inspect.OpenStore(inspect.Options{
		MetaPath:      filepath.Join(r.dataDir, "slotmeta"),
		MessagePath:   filepath.Join(r.dataDir, "messages"),
		HashSlotCount: r.hashSlotCount,
	})
	if err != nil {
		return managementusecase.DBInspectQueryResponse{}, managementusecase.ErrDBInspectUnavailable
	}
	defer store.Close()

	result, err := store.Query(ctx, req.Query)
	if err != nil {
		return managementusecase.DBInspectQueryResponse{}, err
	}
	rows := make([]managementusecase.DBInspectRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		next := make(managementusecase.DBInspectRow, len(row))
		for key, value := range row {
			next[key] = value
		}
		rows = append(rows, next)
	}
	nodeID := req.NodeID
	if nodeID == 0 {
		nodeID = r.nodeID
	}
	return managementusecase.DBInspectQueryResponse{
		NodeID:      nodeID,
		GeneratedAt: r.now(),
		Rows:        rows,
		Stats: managementusecase.DBInspectStats{
			ScanMode:         result.Stats.ScanMode,
			ScannedHashSlots: append([]uint16(nil), result.Stats.ScannedHashSlots...),
			ScannedRows:      result.Stats.ScannedRows,
			ReturnedRows:     result.Stats.ReturnedRows,
			HasMore:          result.Stats.HasMore,
			NextCursor:       result.Stats.NextCursor,
		},
	}, nil
}
