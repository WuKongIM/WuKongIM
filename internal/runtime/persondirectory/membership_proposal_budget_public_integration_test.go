//go:build integration

package persondirectory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/propose"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/require"
)

type proposalStageFunc func(string, string, time.Duration)

func (f proposalStageFunc) ObserveChannelAppendStage(stage, result string, d time.Duration) {
	f(stage, result, d)
}

// A real returned membership proposal may start Ready while other proposals
// still own all ten credits. Ready must acquire one of the same credits.
func TestDirectoryReadySharesMembershipProposalBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	node, err := cluster.New(cluster.Config{NodeID: 1, ListenAddr: addr, DataDir: t.TempDir(),
		Control: cluster.ControlConfig{ClusterID: "directory-proposal-budget", AllowBootstrap: true,
			Voters: []cluster.ControlVoter{{NodeID: 1, Addr: addr}}},
		Slots: cluster.SlotConfig{InitialSlotCount: 12, HashSlotCount: 256, ReplicaCount: 1}})
	require.NoError(t, err)
	first, release := make(chan struct{}), make(chan struct{})
	plateau, replenished := make(chan struct{}), make(chan struct{})
	readyCommitted := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var calls sync.WaitGroup
	var expired, invalid, overlap atomic.Bool
	var held, entered atomic.Int32
	var checksComplete bool
	t.Cleanup(func() {
		unblock()
		cancel()
		calls.Wait()
		stop, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		joined := node.Stop(stop) == nil
		valid := joined && checksComplete && !expired.Load() && !invalid.Load()
		report := map[string]any{"valid": valid, "behavior_red": valid && overlap.Load(),
			"cleanup_joined": joined, "checked_concurrency": 10, "physical_slots": 12, "hash_slots": 256}
		dir := os.Getenv("WK_PERSON_DIRECTORY_TEST_REPORT_DIR")
		if dir == "" {
			dir = t.TempDir()
		}
		output, err := os.OpenFile(filepath.Join(dir, t.Name()+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		encErr := json.NewEncoder(output).Encode(report)
		require.NoError(t, output.Close())
		require.NoError(t, encErr)
		require.True(t, valid, "invalid control or cleanup")
	})
	require.NoError(t, node.Start(ctx))
	require.Eventually(t, func() bool { return node.ProbeWriteReady(ctx) == nil }, 25*time.Second, 25*time.Millisecond)

	task := cohortTasks("proposal-budget", 1)[0]
	require.Equal(t, []error{nil}, node.AdmitPersonDirectoryTasks(ctx, []metadb.PersonDirectoryTask{task}))
	route, err := node.RouteKey(task.ChannelID)
	require.NoError(t, err)
	hashSlot := metadb.HashSlot(route.HashSlot)
	tasks, _, _, err := node.ListPersonDirectoryTaskPage(ctx, hashSlot, metadb.PersonDirectoryTaskCursor{}, 64)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	location := metadb.PersonDirectoryTaskLocation{HashSlot: hashSlot, ChannelID: task.ChannelID, ChannelType: 1, Generation: tasks[0].Generation}
	left, right, err := channelid.DecodePersonChannel(task.ChannelID)
	require.NoError(t, err)
	require.Equal(t, []error{nil, nil}, node.EnsureUserChannelMembershipBatch(ctx, []metadb.UserChannelMembership{
		{UID: left, ChannelID: task.ChannelID, ChannelType: 1, JoinSeq: 1, SourceVersion: location.Generation, UpdatedAt: 1},
		{UID: right, ChannelID: task.ChannelID, ChannelType: 1, JoinSeq: 1, SourceVersion: location.Generation, UpdatedAt: 1},
	}))

	memberships := make([]metadb.UserChannelMembership, 0, 12)
	slots := make(map[uint32]bool)
	for i := 0; i < 4096 && len(memberships) < 12; i++ {
		uid := fmt.Sprintf("budget-pressure-%d", i)
		route, err := node.RouteKey(uid)
		require.NoError(t, err)
		if slots[route.SlotID] {
			continue
		}
		slots[route.SlotID] = true
		memberships = append(memberships, metadb.UserChannelMembership{
			UID: uid, ChannelID: channelid.EncodePersonChannel(uid, uid+"-peer"), ChannelType: 1,
			JoinSeq: 1, SourceVersion: 1, UpdatedAt: 1,
		})
	}
	require.Len(t, memberships, 12)

	memberCtx := propose.WithStageObserver(ctx, proposalStageFunc(func(stage, result string, _ time.Duration) {
		if stage != "meta_create_slot_propose_wait" {
			return
		}
		if result != "ok" {
			invalid.Store(true)
			return
		}
		held.Add(1)
		defer held.Add(-1)
		position := entered.Add(1)
		if position == 10 {
			close(plateau)
		} else if position == 11 {
			close(replenished)
		}
		resume := release
		if position == 1 {
			resume = first
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-resume:
		case <-release:
		case <-ctx.Done():
			expired.Store(true)
		case <-timer.C:
			expired.Store(true)
		}
	}))

	done := make(chan []error, 1)
	calls.Add(1)
	go func() {
		defer calls.Done()
		var errs []error
		var emitted int
		var advance sync.Once
		observe := func(waveCtx context.Context, indices []int, err error) {
			emitted += len(indices)
			errs = append(errs, err)
			advance.Do(func() {
				select {
				case <-replenished:
				case <-ctx.Done():
					invalid.Store(true)
					return
				}
				if held.Load() != 10 {
					invalid.Store(true)
					return
				}
				readyCtx := propose.WithStageObserver(waveCtx, proposalStageFunc(func(stage, result string, _ time.Duration) {
					if stage == "meta_create_slot_propose_wait" && result == "ok" {
						overlap.Store(held.Load() == 10)
						close(readyCommitted)
					}
				}))
				errs = append(errs, node.CompletePersonDirectoryTasks(readyCtx, []metadb.PersonDirectoryTaskLocation{location})...)
			})
		}
		switch waves := any(node).(type) {
		case interface {
			EnsureUserChannelMembershipWaves(context.Context, []metadb.UserChannelMembership, func(context.Context, []int, error))
		}:
			waves.EnsureUserChannelMembershipWaves(memberCtx, memberships, observe)
		case interface {
			EnsureUserChannelMembershipWaves(context.Context, []metadb.UserChannelMembership, func([]int, error))
		}:
			waves.EnsureUserChannelMembershipWaves(memberCtx, memberships, func(indices []int, err error) {
				observe(memberCtx, indices, err)
			})
		default:
			invalid.Store(true)
		}
		if emitted != len(memberships) {
			invalid.Store(true)
		}
		done <- errs
	}()
	select {
	case <-plateau:
	case <-time.After(time.Second):
		t.Fatal("invalid: membership wave did not hold ten real proposal returns")
	}
	require.EqualValues(t, 10, held.Load())
	close(first)
	select {
	case <-replenished:
	case <-time.After(time.Second):
		t.Fatal("invalid: membership wave did not refill the released credit")
	}
	select {
	case <-readyCommitted:
	case <-time.After(400 * time.Millisecond):
	}
	unblock()
	require.NoError(t, errorsJoinWithin(t, ctx, done))
	metadata, err := node.GetChannelMetadataAuthoritative(ctx, task.ChannelID, 1)
	require.NoError(t, err)
	require.Equal(t, metadb.DirectoryProjectionReady, metadata.DirectoryProjectionState)
	checksComplete = true
	require.False(t, overlap.Load(), "Ready ran while all ten membership proposal credits were held")
}

func errorsJoinWithin(t *testing.T, ctx context.Context, done <-chan []error) error {
	t.Helper()
	select {
	case errs := <-done:
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
