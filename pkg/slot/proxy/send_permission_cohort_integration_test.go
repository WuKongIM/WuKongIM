//go:build integration

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

// Failure cases precede implementation in the permission-cohorts plan. These
// tests control seal/barrier instants through the real proxy and DB snapshot;
// only the cluster ports are controlled. Real-network proof is separate E2E.
func TestSendPermissionCohorts(t *testing.T) {
	started := time.Now().UTC()
	var cases []string
	t.Cleanup(func() {
		path := os.Getenv("WK_PERMISSION_COHORT_REPORT")
		if path == "" {
			path = filepath.Join(t.TempDir(), "permission-cohorts.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "source_revision": os.Getenv("WK_VALIDATION_SOURCE_REVISION"), "kind": "controlled cluster ports; production proxy/codec/database", "cases": cases}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0644))
		t.Logf("cohort report: %s", path)
	})
	t.Run("dedup-and-align-local-and-remote", func(t *testing.T) {
		for _, remote := range []bool{false, true} {
			t.Run(fmt.Sprintf("remote-%t", remote), func(t *testing.T) {
				db := boundaryDB(t)
				wb := db.NewWriteBatch()
				_, err := wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1})
				require.NoError(t, err)
				require.NoError(t, wb.UpsertChannel(2, metadb.Channel{ChannelID: "g", ChannelType: 2, SendBan: 1}))
				require.NoError(t, wb.Commit())
				require.NoError(t, wb.Close())
				owner := newBoundaryCluster(2)
				owner.barrier = func(ctx context.Context, _ multiraft.SlotID) error { owner.barriers.Add(1); return ctx.Err() }
				serving := New(owner, db)
				origin := newBoundaryCluster(1)
				origin.rpc = func(ctx context.Context, _ multiraft.NodeID, p []byte) ([]byte, error) {
					return serving.handleSendPermissionRPC(ctx, p)
				}
				caller := serving
				if remote {
					caller = New(origin, db)
				}
				caller.permissionCohortWindow = time.Hour // full membership closes the window
				t.Cleanup(caller.CloseSendPermissionReads)
				reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}, {Kind: PermissionMetadataReadChannel, ChannelID: "g", ChannelType: 2}, {Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
				pending := make([]<-chan []PermissionMetadataReadResult, 64)
				for i := range pending {
					pending[i] = startBoundaryRead(t, caller, reads)
				}
				for _, p := range pending {
					out := awaitBoundaryResult(t, p)
					require.Len(t, out, 3)
					for _, r := range out {
						require.NoError(t, r.Err)
						require.True(t, r.Found)
					}
					require.EqualValues(t, 1, out[0].UserPolicy.SendBan)
					require.EqualValues(t, 1, out[1].Channel.SendBan)
					require.Equal(t, out[0], out[2])
				}
				require.EqualValues(t, 2, owner.barriers.Load(), "distinct Slots retain distinct fresh barriers")
				if remote {
					require.EqualValues(t, 1, origin.calls.Load())
				} else {
					require.Zero(t, owner.calls.Load())
				}
				requireCohortDrained(t, caller)
			})
		}
		cases = append(cases, t.Name())
	})
	t.Run("deadline-and-cancel-isolation", func(t *testing.T) {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit-%t", explicit), func(t *testing.T) {
				db := boundaryDB(t)
				c := newBoundaryCluster(2)
				gate := boundaryGate(t)
				entered := make(chan struct{}, 2)
				c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
					c.barriers.Add(1)
					entered <- struct{}{}
					select {
					case <-gate:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				s := New(c, db)
				s.permissionCohortWindow = time.Hour
				t.Cleanup(s.CloseSendPermissionReads)
				reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
				short, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
				defer cancel()
				first := launchCohortRead(s, short, reads)
				long := startBoundaryRead(t, s, reads)
				sealTestCohort(t, s, 2)
				awaitBoundaryEvent(t, entered)
				if explicit {
					cancel()
				}
				out := awaitBoundaryResult(t, first)
				want := context.DeadlineExceeded
				if explicit {
					want = context.Canceled
				}
				require.ErrorIs(t, out[0].Err, want)
				select {
				case <-long:
					t.Fatal("one caller poisoned the other")
				default:
				}
				closeBoundaryGate(gate)
				require.NoError(t, awaitBoundaryResult(t, long)[0].Err)
				require.EqualValues(t, 1, c.barriers.Load())
				requireCohortDrained(t, s)
			})
		}
		cases = append(cases, t.Name())
	})
	t.Run("late-arrival-cannot-join-started-barrier", func(t *testing.T) {
		db := boundaryDB(t)
		c := newBoundaryCluster(2)
		gate := boundaryGate(t)
		entered := make(chan struct{}, 2)
		c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
			n := c.barriers.Add(1)
			entered <- struct{}{}
			if n == 1 {
				select {
				case <-gate:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		}
		s := New(c, db)
		s.permissionCohortWindow = time.Hour
		t.Cleanup(s.CloseSendPermissionReads)
		reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
		old := startBoundaryRead(t, s, reads)
		sealTestCohort(t, s, 1)
		awaitBoundaryEvent(t, entered)
		wb := db.NewWriteBatch()
		_, err := wb.ApplySendBan(1, metadb.SendBanMutation{UID: "u", SendBan: 1})
		require.NoError(t, err)
		require.NoError(t, wb.Commit())
		require.NoError(t, wb.Close())
		late := startBoundaryRead(t, s, reads)
		sealTestCohort(t, s, 2)
		awaitBoundaryEvent(t, entered)
		result := awaitBoundaryResult(t, late)
		require.NoError(t, result[0].Err)
		require.EqualValues(t, 1, result[0].UserPolicy.SendBan)
		require.EqualValues(t, 2, c.barriers.Load())
		closeBoundaryGate(gate)
		require.NoError(t, awaitBoundaryResult(t, old)[0].Err)
		requireCohortDrained(t, s)
		cases = append(cases, t.Name())
	})
	t.Run("last-cancel-and-close-join", func(t *testing.T) {
		for _, closeStore := range []bool{false, true} {
			t.Run(fmt.Sprintf("close-%t", closeStore), func(t *testing.T) {
				db := boundaryDB(t)
				c := newBoundaryCluster(2)
				entered := make(chan struct{}, 1)
				var active atomic.Int64
				c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
					active.Add(1)
					defer active.Add(-1)
					entered <- struct{}{}
					<-ctx.Done()
					return ctx.Err()
				}
				s := New(c, db)
				s.permissionCohortWindow = time.Hour
				t.Cleanup(s.CloseSendPermissionReads)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
				first := launchCohortRead(s, ctx, reads)
				sealTestCohort(t, s, 1)
				awaitBoundaryEvent(t, entered)
				if closeStore {
					s.CloseSendPermissionReads()
					s.CloseSendPermissionReads()
				} else {
					cancel()
				}
				out := awaitBoundaryResult(t, first)
				require.Error(t, out[0].Err)
				require.Zero(t, active.Load())
				require.Zero(t, c.admitted.Load())
				requireCohortDrained(t, s)
				if closeStore {
					require.Error(t, s.ReadSendPermissionMetadataBatch(context.Background(), reads)[0].Err)
				}
			})
		}
		cases = append(cases, t.Name())
	})
	t.Run("retained-bytes-reject-and-recover", func(t *testing.T) {
		db := boundaryDB(t)
		c := newBoundaryCluster(2)
		gate := boundaryGate(t)
		entered := make(chan struct{}, 64)
		c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
			entered <- struct{}{}
			select {
			case <-gate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		s := New(c, db)
		s.permissionCohortWindow = time.Hour
		t.Cleanup(s.CloseSendPermissionReads)
		var pending []<-chan []PermissionMetadataReadResult
		// Each accepted caller retains a real large input slice; the budget must
		// reject before copying another input, even with spare worker/call positions.
		reads := make([]PermissionMetadataRead, 32)
		for i := range reads {
			reads[i] = PermissionMetadataRead{Kind: PermissionMetadataReadUserSendPolicy, UID: fmt.Sprintf("%02d", i) + strings.Repeat("x", 32<<10)}
		}
		for i := 0; i < 20; i++ {
			p := launchCohortRead(s, context.Background(), reads)
			select {
			case out := <-p:
				require.ErrorIs(t, out[0].Err, ErrPermissionBusy)
				require.NotEmpty(t, pending)
				closeBoundaryGate(gate)
				for _, wait := range pending {
					require.NoError(t, awaitBoundaryResult(t, wait)[0].Err)
				}
				requireCohortDrained(t, s)
				s.permissionCohortWindow = 0
				require.NoError(t, s.ReadSendPermissionMetadataBatch(context.Background(), []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}})[0].Err)
				cases = append(cases, t.Name())
				return
			case <-time.After(20 * time.Millisecond):
				pending = append(pending, p)
				sealTestCohort(t, s, len(pending))
				awaitBoundaryEvent(t, entered)
			}
		}
		t.Fatal("retained memory was not bounded")
	})

	t.Run("owned-call-and-active-cohort-limits", func(t *testing.T) {
		for _, callLimit := range []bool{false, true} {
			t.Run(fmt.Sprintf("calls-%t", callLimit), func(t *testing.T) {
				db := boundaryDB(t)
				c := newBoundaryCluster(2)
				gate := boundaryGate(t)
				entered := make(chan struct{}, 64)
				c.barrier = func(ctx context.Context, _ multiraft.SlotID) error {
					entered <- struct{}{}
					select {
					case <-gate:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				s := New(c, db)
				s.permissionCohortWindow = time.Hour
				t.Cleanup(s.CloseSendPermissionReads)
				reads := []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}}
				total, width := 64, 1
				if callLimit {
					total, width = 1024, 64
				}
				pending := make([]<-chan []PermissionMetadataReadResult, 0, total)
				for i := 0; i < total; i += width {
					for j := 0; j < width; j++ {
						pending = append(pending, startBoundaryRead(t, s, reads))
					}
					if !callLimit {
						sealTestCohort(t, s, len(pending))
					}
					awaitBoundaryEvent(t, entered)
				}
				out := s.ReadSendPermissionMetadataBatch(context.Background(), reads)
				require.ErrorIs(t, out[0].Err, ErrPermissionBusy)
				closeBoundaryGate(gate)
				for _, p := range pending {
					require.NoError(t, awaitBoundaryResult(t, p)[0].Err)
				}
				requireCohortDrained(t, s)
			})
		}
		cases = append(cases, t.Name())
	})
	t.Run("pre-canceled-and-invalid-do-not-own-work", func(t *testing.T) {
		db := boundaryDB(t)
		c := newBoundaryCluster(2)
		s := New(c, db)
		t.Cleanup(s.CloseSendPermissionReads)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out := s.ReadSendPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy, UID: "u"}})
		require.ErrorIs(t, out[0].Err, context.Canceled)
		out = s.ReadSendPermissionMetadataBatch(context.Background(), []PermissionMetadataRead{{Kind: PermissionMetadataReadUserSendPolicy}})
		require.Error(t, out[0].Err)
		require.Zero(t, c.barriers.Load())
		requireCohortDrained(t, s)
		cases = append(cases, t.Name())
	})
}

func launchCohortRead(s *Store, ctx context.Context, reads []PermissionMetadataRead) <-chan []PermissionMetadataReadResult {
	done := make(chan []PermissionMetadataReadResult, 1)
	go func() { done <- s.ReadSendPermissionMetadataBatch(ctx, reads) }()
	return done
}
func sealTestCohort(t *testing.T, s *Store, owned int) {
	t.Helper()
	require.Eventually(t, func() bool {
		s.permissionCohortMu.Lock()
		defer s.permissionCohortMu.Unlock()
		return s.permissionCohortRequests == owned && s.permissionCollecting != nil
	}, time.Second, time.Millisecond)
	s.permissionCohortMu.Lock()
	s.sealSendPermissionCohortLocked(s.permissionCollecting)
	s.permissionCohortMu.Unlock()
}
func requireCohortDrained(t *testing.T, s *Store) {
	t.Helper()
	require.Eventually(t, func() bool {
		s.permissionCohortMu.Lock()
		defer s.permissionCohortMu.Unlock()
		return len(s.permissionCohorts) == 0 && s.permissionCohortRequests == 0 && s.permissionCohortBytes == 0
	}, time.Second, time.Millisecond)
	require.Zero(t, s.permissionInflight.Load())
	require.Zero(t, s.permissionWaiting.Load())
}
