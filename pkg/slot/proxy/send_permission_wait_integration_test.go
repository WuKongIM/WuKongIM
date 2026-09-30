//go:build integration

package proxy

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// Failure cases precede the implementation in the admission-wait report.
// These tests exercise the real receiver and snapshot with controlled barriers.
func TestSendPermissionBoundedWait(t *testing.T) {
	hold := func(t *testing.T, s *Store) []func() {
		t.Helper()
		var releases []func()
		for range sendPermissionMaxExecuting {
			release, err := s.acquireSendPermissionEnvelope(context.Background(), 0)
			require.NoError(t, err)
			var once sync.Once
			owned := func() { once.Do(release) }
			t.Cleanup(owned)
			releases = append(releases, owned)
		}
		return releases
	}
	t.Run("queued-read-observes-new-ban", func(t *testing.T) {
		db := boundaryDB(t)
		c := newBoundaryCluster(2)
		s := New(c, db)
		releases := hold(t, s)
		route := c.SendPermissionRoutes([]string{"u"})[0]
		q := sendPermissionRequest{Format: 1, Groups: []sendPermissionGroup{{Fence: route.Fence, Reads: []sendPermissionRead{{Index: 0, HashSlot: route.HashSlot, Read: PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}}}}}
		raw, err := json.Marshal(q)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		type answer struct {
			body []byte
			err  error
		}
		done := make(chan answer, 1)
		go func() { body, e := s.handleSendPermissionRPC(ctx, raw); done <- answer{body, e} }()
		joined := false
		t.Cleanup(func() {
			cancel()
			if !joined {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("receiver did not join")
				}
			}
		})
		require.Eventually(t, func() bool { return s.permissionWaiting.Load() == 1 }, 50*time.Millisecond, time.Millisecond)
		require.Zero(t, c.admitted.Load(), "waiting cannot enter maintenance/barrier work")
		wb := db.NewWriteBatch()
		_, err = wb.ApplySendBan(metadb.HashSlot(route.HashSlot), metadb.SendBanMutation{UID: "u", SendBan: 1})
		require.NoError(t, err)
		require.NoError(t, wb.Commit())
		require.NoError(t, wb.Close())
		releases[0]()
		var got answer
		select {
		case got = <-done:
			joined = true
		case <-time.After(time.Second):
			t.Fatal("queued receiver did not finish")
		}
		require.NoError(t, got.err)
		var reply sendPermissionReply
		require.NoError(t, decodeSendPermissionJSON(got.body, &reply))
		require.NoError(t, validateSendPermissionReply(q, reply))
		require.EqualValues(t, 1, reply.Groups[0].Results[0].Fact.UserPolicy.SendBan)
		require.Zero(t, s.permissionWaiting.Load())
		require.EqualValues(t, sendPermissionMaxExecuting-1, s.permissionInflight.Load())
	})
	t.Run("queue-cap-cancellation-and-permit-reuse", func(t *testing.T) {
		s := New(newBoundaryCluster(2), boundaryDB(t))
		releases := hold(t, s)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		first, cancelFirst := context.WithCancel(ctx)
		defer cancelFirst()
		var wg sync.WaitGroup
		t.Cleanup(func() { cancel(); wg.Wait() })
		launch := func(ctx context.Context) <-chan error {
			done := make(chan error, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				release, err := s.acquireSendPermissionEnvelope(ctx, 0)
				if err == nil {
					release()
				}
				done <- err
			}()
			return done
		}
		pending := []<-chan error{launch(first)}
		for range sendPermissionMaxWaiting - 1 {
			pending = append(pending, launch(ctx))
		}
		require.Eventually(t, func() bool { return s.permissionWaiting.Load() == sendPermissionMaxWaiting }, 5*time.Second, time.Millisecond)
		body, err := s.handleSendPermissionRPC(ctx, []byte("invalid JSON"))
		require.NoError(t, err, "full queue rejects before decoding")
		var busy sendPermissionReply
		require.NoError(t, decodeSendPermissionJSON(body, &busy))
		require.ErrorIs(t, validateSendPermissionReply(sendPermissionRequest{}, busy), ErrPermissionBusy)
		require.EqualValues(t, sendPermissionMaxExecuting, s.permissionInflight.Load())
		cancelFirst()
		require.ErrorIs(t, <-pending[0], context.Canceled)
		require.EqualValues(t, sendPermissionMaxWaiting-1, s.permissionWaiting.Load())
		replacement := launch(ctx)
		require.Eventually(t, func() bool { return s.permissionWaiting.Load() == sendPermissionMaxWaiting }, 5*time.Second, time.Millisecond)
		releases[0]()
		for _, done := range pending[1:] {
			require.NoError(t, <-done)
		}
		require.NoError(t, <-replacement)
		require.Zero(t, s.permissionWaiting.Load())
		require.EqualValues(t, sendPermissionMaxExecuting-1, s.permissionInflight.Load())
	})
	t.Run("wait-deadline-and-caller-deadline", func(t *testing.T) {
		s := New(newBoundaryCluster(2), boundaryDB(t))
		hold(t, s)
		started := time.Now()
		release, err := s.acquireSendPermissionEnvelope(context.Background(), 0)
		require.Nil(t, release)
		require.ErrorIs(t, err, ErrPermissionBusy)
		require.GreaterOrEqual(t, time.Since(started), sendPermissionMaxWait-10*time.Millisecond)
		require.Less(t, time.Since(started), sendPermissionMaxWait+time.Second)
		require.Zero(t, s.permissionWaiting.Load())
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		release, err = s.acquireSendPermissionEnvelope(ctx, 0)
		require.Nil(t, release)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Zero(t, s.permissionWaiting.Load())
		require.EqualValues(t, sendPermissionMaxExecuting, s.permissionInflight.Load())
	})
}
