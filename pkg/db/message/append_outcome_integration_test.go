//go:build integration

package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestExactAppendOutcomeUnknownResolvesAfterReopenAtAtomicCommitBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		commit      func(*engine.Batch) error
		wantReplay  quorumlog.AppendOutcome
		wantDurable bool
	}{
		{
			name: "crash_before_physical_commit",
			commit: func(*engine.Batch) error {
				return errors.New("injected crash before commit")
			},
			wantReplay: quorumlog.AppendOutcomeDurable,
		},
		{
			name: "response_lost_after_physical_commit",
			commit: func(batch *engine.Batch) error {
				if err := batch.Commit(true); err != nil {
					return err
				}
				return errors.New("injected response loss after commit")
			},
			wantReplay:  quorumlog.AppendOutcomeAlreadyDurable,
			wantDurable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir()
			db, err := Open(path)
			if err != nil {
				t.Fatalf("Open(): %v", err)
			}
			key := channel.ChannelKey("append-outcome:1")
			id := channel.ChannelID{ID: "append-outcome", Type: 1}
			store, err := db.ForChannel(key, id)
			if err != nil {
				t.Fatalf("ForChannel(): %v", err)
			}
			record := compatExactTestRecord(t, 5, 8101, id.ID, "client-1")
			manifest := sealCompatProposalManifest(t, DurableProposalManifest{
				Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
				CommandID: [32]byte{8, 1, 0, 1}, BaseOffset: 0, LastOffset: 1,
			}, []channel.Record{record})
			req := AppendBatchItem{
				Store: store, Records: []channel.Record{record}, ExactBaseOffset: true,
				ExpectedBaseOffset: 0, Proposal: manifest,
			}

			db.committer.SetCommitFunc(test.commit)
			first := StoreAppendBatch(context.Background(), []AppendBatchItem{req})
			if len(first) != 1 || first[0].Err == nil || first[0].Outcome != quorumlog.AppendOutcomeUnknown {
				t.Fatalf("StoreAppendBatch() = %+v, want outcome unknown", first)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("store.Close(): %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("Close(): %v", err)
			}

			db, err = Open(path)
			if err != nil {
				t.Fatalf("Open() after injected crash: %v", err)
			}
			defer db.Close()
			store, err = db.ForChannel(key, id)
			if err != nil {
				t.Fatalf("ForChannel() after injected crash: %v", err)
			}
			defer store.Close()
			req.Store = store
			replay := StoreAppendBatch(context.Background(), []AppendBatchItem{req})
			if len(replay) != 1 || replay[0].Err != nil || replay[0].Outcome != test.wantReplay {
				t.Fatalf("replay StoreAppendBatch() = %+v, want outcome %v", replay, test.wantReplay)
			}
			if got := store.LEO(); got != 1 {
				t.Fatalf("LEO after replay = %d, want 1", got)
			}
			if test.wantDurable && replay[0].Outcome != quorumlog.AppendOutcomeAlreadyDurable {
				t.Fatal("post-commit response loss did not preserve exact durable identity")
			}
		})
	}
}
