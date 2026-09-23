//go:build integration

package message

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestMQTTCheckpointAdvanceFencesConcurrentSuffixTruncation(t *testing.T) {
	for _, compat := range []bool{false, true} {
		t.Run(map[bool]string{false: "typed", true: "compatibility"}[compat], func(t *testing.T) {
			e := openCompatEngine(t)
			s := mustForChannel(t, e, "mqtt-truncate:1", channel.ChannelID{ID: "mqtt-truncate", Type: 1})
			defer s.Close()
			ctx := context.Background()
			if _, err := s.log.Append(ctx, []Record{{ID: 1, Payload: []byte("one")}, {ID: 2, Payload: []byte("two")}, {ID: 3, Payload: []byte("three")}}, AppendOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.log.StoreCheckpoint(ctx, Checkpoint{HW: 2}); err != nil {
				t.Fatal(err)
			}
			if err := s.log.ApplyMQTTSourceState(ctx, 0, MQTTSourceState{Generation: "source", Revision: 1}); err != nil {
				t.Fatal(err)
			}
			s.log.checkpointMu.Lock()
			locked := true
			defer func() {
				if locked {
					s.log.checkpointMu.Unlock()
				}
			}()
			done := make(chan error, 1)
			go func() {
				if compat {
					done <- s.Truncate(2)
				} else {
					done <- s.log.TruncateFrom(ctx, 3)
				}
			}()
			select {
			case err := <-done:
				t.Fatalf("suffix cut bypassed an in-progress checkpoint commit: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			// Complete the already-owned checkpoint mutation while the cut waits.
			setPhysicalTestValue(t, e, encodeCheckpointKey(s.log.key), encodeCheckpoint(Checkpoint{HW: 3}))
			s.log.checkpointMu.Unlock()
			locked = false
			select {
			case err := <-done:
				if !errors.Is(err, dberrors.ErrConflict) && !errors.Is(err, channel.ErrCorruptState) {
					t.Fatalf("cut did not recheck committed frontier: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cut did not release its locks")
			}
			rows, err := s.log.ReadMQTTProtectedSource(ctx, "source", 1, 3, ReadOptions{Limit: 3, MaxBytes: 1024})
			if err != nil || len(rows) != 3 {
				t.Fatalf("newly committed source content lost: rows=%d err=%v", len(rows), err)
			}
		})
	}
}
