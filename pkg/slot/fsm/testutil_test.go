package fsm

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	raftstorage "github.com/WuKongIM/WuKongIM/pkg/raftlog"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	testTickInterval = 10 * time.Millisecond
	testElectionTick = 10
	testWaitTimeout  = testTickInterval * time.Duration(testElectionTick*20)
	testPollInterval = testTickInterval
)

func mustNewStateMachine(t testing.TB, db *metadb.DB, slot uint64) multiraft.StateMachine {
	t.Helper()

	sm, err := NewStateMachine(db, slot)
	if err != nil {
		t.Fatalf("NewStateMachine() error = %v", err)
	}
	return sm
}

func openTestDB(t testing.TB) *metadb.DB {
	t.Helper()

	return openTestDBAt(t, filepath.Join(t.TempDir(), "db"))
}

func openTestDBAt(t testing.TB, path string) *metadb.DB {
	t.Helper()

	db, err := metadb.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		defer recoverDoubleClose()
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return db
}

func openTestRaftDBAt(t testing.TB, path string) *raftstorage.DB {
	t.Helper()

	db, err := raftstorage.Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		defer recoverDoubleClose()
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return db
}

// recoverDoubleClose catches panics from closing an already-closed database.
// The underlying storage engine (Pebble) may panic when Close is called
// on a DB that was already closed by the test. We detect this via
// isClosedPanic. All other panics are re-raised so real bugs surface.
func recoverDoubleClose() {
	r := recover()
	if r == nil {
		return
	}
	if isClosedPanic(r) {
		return
	}
	panic(r)
}

// isClosedPanic reports whether the recovered panic value indicates a
// double-close on an already-closed database.
func isClosedPanic(r any) bool {
	switch v := r.(type) {
	case error:
		return strings.Contains(v.Error(), "closed")
	case string:
		return strings.Contains(v, "closed")
	default:
		return false
	}
}

func newStartedRuntime(t testing.TB) *multiraft.Runtime {
	t.Helper()

	rt, err := multiraft.New(multiraft.Options{
		NodeID:       1,
		TickInterval: testTickInterval,
		Workers:      1,
		Transport:    fakeTransport{},
		Raft: multiraft.RaftOptions{
			ElectionTick:  testElectionTick,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return rt
}

type fakeTransport struct{}

func (fakeTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	return nil
}

func waitForCondition(t testing.TB, fn func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(testWaitTimeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(testPollInterval)
	}
	t.Fatalf("condition not satisfied before timeout: %s", msg)
}
