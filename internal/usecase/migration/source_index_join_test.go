package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

type countedIndexWorkspace struct {
	Workspace
	indexGets int
}

func (w *countedIndexWorkspace) Get(ctx context.Context, key []byte) ([]byte, bool, error) {
	if strings.Contains(string(key), "/expected/") || strings.Contains(string(key), "/actual/") {
		w.indexGets++
	}
	return w.Workspace.Get(ctx, key)
}

type mergedIndexWorkspace struct {
	*countedIndexWorkspace
	spool *transfer.Spool
	walks int
}

func (w *mergedIndexWorkspace) WalkMerge(ctx context.Context, left, right []byte, visit func(transfer.SpoolRow, transfer.SpoolRow) error) error {
	w.walks++
	return w.spool.WalkMerge(ctx, left, right, visit)
}

func TestSourceIndexJoinMergeMatchesPointValidation(t *testing.T) {
	for _, mode := range []string{"point", "merge"} {
		for _, scenario := range []string{"matched", "missing", "wrong-value", "orphan", "older-sender", "quarantined-primary"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				ctx := context.Background()
				s, err := transfer.OpenSpool(filepath.Join(t.TempDir(), "spool"), "join-test", 4096)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, s.Close()) })
				counted := &countedIndexWorkspace{Workspace: s}
				merged := &mergedIndexWorkspace{countedIndexWorkspace: counted, spool: s}
				var w Workspace = counted
				if mode == "merge" {
					w = merged
				}
				base := "source-index/join-test/"
				key := func(phase string, k []byte) []byte {
					return []byte(fmt.Sprintf("%s%s/%020d/%04d/%x", base, phase, 1, 0, k))
				}
				put := func(k []byte, value any) {
					data, err := json.Marshal(value)
					require.NoError(t, err)
					require.NoError(t, s.Put(ctx, []transfer.SpoolRow{{Key: k, Value: data}}))
				}
				entry := SourceIndexEntry{Key: []byte("a"), Value: []byte("value")}
				want := ""
				switch scenario {
				case "matched", "wrong-value":
					put(key("expected", entry.Key), entry)
					if scenario == "wrong-value" {
						entry.Value = []byte("different")
						want = "points to a different primary row"
					}
				case "missing":
					// An earlier orphan must not mask a later missing expected key.
					put(key("expected", []byte("z")), SourceIndexEntry{Key: []byte("z")})
					want = "source business index is missing"
				case "orphan":
					want = "orphaned or disagrees"
				case "older-sender":
					entry.SenderKey, entry.SenderSeq = []byte("sender"), 2
					put(key("sender-max", entry.SenderKey), uint64(3))
				case "quarantined-primary":
					entry.PrimaryKey, entry.AllowAbsentPrimary = []byte("primary"), true
					primary := sourceRowKey(1, Row{Shard: 0, Key: entry.PrimaryKey})
					put(primary, "original hidden row remains in raw capture")
					w = &quarantineWorkspace{Workspace: w, hidden: map[string]bool{string(primary): true}}
				}
				put(key("actual", entry.Key), sourceIndexRecord{NodeID: 1, Shard: 0, Table: "Message", Entry: entry})
				err = validateCapturedIndexJoin(ctx, w, base)
				if want == "" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, want)
				}
				if mode == "merge" {
					require.Equal(t, 1, merged.walks)
					require.Zero(t, counted.indexGets, "the sorted join must not regress to per-index point lookups")
				} else {
					require.Positive(t, counted.indexGets)
				}
			})
		}
	}
}
