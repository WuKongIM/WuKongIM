//go:build integration

package commit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestCoordinatorNativeStatsRespectActualCommitBoundary(t *testing.T) {
	failure := errors.New("controlled terminal error")
	for _, tc := range []struct {
		name                                                       string
		native, empty, buildFailure, commitFailure, publishFailure bool
	}{
		{name: "native success", native: true},
		{name: "native committed then wrapper error", native: true, commitFailure: true},
		{name: "native committed then publish error", native: true, publishFailure: true},
		{name: "fake success"},
		{name: "fake error", commitFailure: true},
		{name: "build rejected", native: true, buildFailure: true},
		{name: "empty native call", native: true, empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			observer := &recordingObserver{}
			c := commit.NewCoordinator(db, commit.Config{FlushWindow: time.Millisecond, MaxRequests: 1, Observer: observer})
			defer c.Close()
			var captured engine.NativeCommitStats
			nativeCalls := 0
			c.SetCommitFunc(func(batch *engine.Batch) error {
				if tc.native {
					nativeCalls++
					if err := batch.Commit(false); err != nil {
						return err
					}
					captured = batch.CommitStats()
				}
				if tc.commitFailure {
					return failure
				}
				return nil
			})
			err := c.Submit(context.Background(), commit.Request{
				Records: 1, Bytes: 10,
				Build: func(batch *engine.Batch) error {
					if tc.buildFailure {
						return failure
					}
					if tc.empty {
						return nil
					}
					return batch.Set([]byte("key"), []byte("value"))
				},
				Publish: func() error {
					if tc.publishFailure {
						return failure
					}
					return nil
				},
			})
			wantFailure := tc.buildFailure || tc.commitFailure || tc.publishFailure
			if errors.Is(err, failure) != wantFailure {
				t.Fatalf("Submit error=%v, want controlled failure=%v", err, wantFailure)
			}
			event := observer.onlyBatch(t)
			wantAvailable := tc.native && !tc.empty && !tc.buildFailure
			if event.Native.Available != wantAvailable {
				t.Fatalf("native availability=%v, want%v; event=%+v", event.Native.Available, wantAvailable, event)
			}
			if event.Native != captured {
				t.Fatalf("native snapshot=%+v, actual engine snapshot=%+v", event.Native, captured)
			}
			if tc.buildFailure && nativeCalls != 0 {
				t.Fatal("build rejection invoked native commit")
			}
			if event.TotalDuration != event.CollectDuration+event.BuildDuration+event.CommitDuration+event.PublishDuration {
				t.Fatalf("native durations changed legacy total: %+v", event)
			}
		})
	}
}

func TestCoordinatorClosedSubmissionDoesNotInventNativeObservation(t *testing.T) {
	observer := &recordingObserver{}
	c := commit.NewCoordinator(openTestDB(t), commit.Config{Observer: observer})
	c.Close()
	err := c.Submit(context.Background(), commit.Request{Build: func(*engine.Batch) error { t.Fatal("closed submission built"); return nil }})
	if !errors.Is(err, commit.ErrClosed) {
		t.Fatalf("Submit error=%v,want closed", err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.batches) != 0 {
		t.Fatalf("closed admission invented physical observations: %+v", observer.batches)
	}
}
