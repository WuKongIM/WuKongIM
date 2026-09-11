package message

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// The blocked first wave proves overlap and the concurrency bound without wall-clock sleeps.
func TestSyncBatchPermissionPreparationBoundedAndAligned(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%v", fail), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				firstErr := errors.New("first input permission failure")
				store := &barrierSyncMembershipStore{release: release, fail: fail, firstErr: firstErr}
				reader := &recordingChannelMessageReader{batchResults: make([]ChannelMessageReadResult, 20)}
				app := New(Options{Reader: reader, Memberships: store})
				query := SyncChannelMessagesBatchQuery{LoginUID: "reader"}
				for i := 0; i < 20; i++ {
					query.Items = append(query.Items, SyncChannelMessagesQuery{ChannelID: fmt.Sprintf("g%d", i), ChannelType: 2})
				}
				var result SyncChannelMessagesBatchResult
				var err error
				go func() { result, err = app.SyncChannelMessagesBatch(context.Background(), query) }()
				synctest.Wait()
				started := store.calls.Load()
				readEarly := reader.batchCalls
				close(release)
				synctest.Wait()
				if started != 8 {
					t.Fatalf("blocked permission calls=%d, want 8 concurrent reads", started)
				}
				if readEarly != 0 {
					t.Fatal("message batch started before all permissions resolved")
				}
				if store.calls.Load() != 20 {
					t.Fatalf("permission calls=%d, want 20", store.calls.Load())
				}
				if fail {
					if !errors.Is(err, firstErr) || reader.batchCalls != 0 {
						t.Fatalf("error=%v reads=%d, want first input error and no message reads", err, reader.batchCalls)
					}
					return
				}
				if err != nil || len(result.Items) != 20 || reader.batchCalls != 1 {
					t.Fatalf("error=%v results=%d reads=%d", err, len(result.Items), reader.batchCalls)
				}
				for i, q := range reader.batchQueries {
					if q.ChannelID.ID != fmt.Sprintf("g%d", i) || q.MinSeq != uint64(i+2) || result.Items[i].ChannelID != q.ChannelID.ID {
						t.Fatalf("index %d lost channel ordering or visibility: %+v", i, q)
					}
				}
			})
		})
	}
}

type barrierSyncMembershipStore struct {
	release  <-chan struct{}
	calls    atomic.Int32
	fail     bool
	firstErr error
}

func (s *barrierSyncMembershipStore) GetUserChannelMembership(_ context.Context, _ string, channelID string, _ int64) (metadb.UserChannelMembership, bool, error) {
	s.calls.Add(1)
	<-s.release
	var i int
	fmt.Sscanf(channelID, "g%d", &i)
	if s.fail && i == 0 {
		return metadb.UserChannelMembership{}, false, s.firstErr
	}
	if s.fail && i == 1 {
		return metadb.UserChannelMembership{}, false, errors.New("second input permission failure")
	}
	return metadb.UserChannelMembership{JoinSeq: 1, DeletedToSeq: uint64(i + 1)}, true, nil
}

func TestSyncBatchCanceledBeforePreparationDoesNotRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &multiSyncMembershipStore{}
	reader := &recordingChannelMessageReader{}
	app := New(Options{Reader: reader, Memberships: store})
	_, err := app.SyncChannelMessagesBatch(ctx, SyncChannelMessagesBatchQuery{LoginUID: "u", Items: []SyncChannelMessagesQuery{{ChannelID: "g", ChannelType: 2}}})
	if !errors.Is(err, context.Canceled) || store.calls.Load() != 0 || reader.batchCalls != 0 {
		t.Fatalf("error=%v permission calls=%d reads=%d", err, store.calls.Load(), reader.batchCalls)
	}
}

func TestSyncBatchTerminalStateStillBlocksAllMessageReads(t *testing.T) {
	reader := &recordingChannelMessageReader{}
	app := New(Options{Reader: reader, Memberships: liveSyncMembershipStore(), ChannelState: staticSyncChannelStateStore{channel: metadb.Channel{Disband: 1}}})
	_, err := app.SyncChannelMessagesBatch(context.Background(), SyncChannelMessagesBatchQuery{LoginUID: "u", Items: []SyncChannelMessagesQuery{{ChannelID: "g1", ChannelType: 2}, {ChannelID: "g2", ChannelType: 2}}})
	if !errors.Is(err, ErrSyncChannelDisbanded) || reader.batchCalls != 0 {
		t.Fatalf("error=%v reads=%d", err, reader.batchCalls)
	}
}
