package management

import (
	"context"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/stretchr/testify/require"
)

func TestListControllerLogEntriesDelegatesNodeScopedRead(t *testing.T) {
	app := New(Options{
		Cluster: fakeClusterReader{
			controllerLogEntries: map[uint64]raftcluster.ControllerLogEntries{
				2: {
					NodeID:       2,
					FirstIndex:   1,
					LastIndex:    4,
					CommitIndex:  4,
					AppliedIndex: 3,
					NextCursor:   3,
					Items: []raftcluster.ControllerLogEntry{{
						Index:        4,
						Term:         2,
						Type:         "normal",
						DataSize:     12,
						DecodeStatus: "ok",
						DecodedType:  "add_slot",
						Decoded: map[string]any{
							"command":     "add_slot",
							"new_slot_id": uint64(9),
						},
					}},
				},
			},
		},
	})

	got, err := app.ListControllerLogEntries(context.Background(), ListControllerLogEntriesRequest{
		NodeID: 2,
		Limit:  1,
		Cursor: 5,
	})
	require.NoError(t, err)
	require.Equal(t, ControllerLogEntriesResponse{
		NodeID:       2,
		FirstIndex:   1,
		LastIndex:    4,
		CommitIndex:  4,
		AppliedIndex: 3,
		NextCursor:   3,
		Items: []ControllerLogEntry{{
			Index:        4,
			Term:         2,
			Type:         "normal",
			DataSize:     12,
			DecodeStatus: "ok",
			DecodedType:  "add_slot",
			Decoded: map[string]any{
				"command":     "add_slot",
				"new_slot_id": uint64(9),
			},
		}},
	}, got)
}
