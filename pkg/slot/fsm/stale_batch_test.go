package fsm

import (
	"context"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

// A stale conditional entry must not turn an otherwise healthy replay suffix
// into one synchronous commit per entry. Repeated writes also prove log order.
func TestStaleBatchPreservesOrderedWritesWithoutPerEntryCommits(t *testing.T) {
	for _, staleAt := range []int{0, 127, 255} {
		t.Run(fmt.Sprint(staleAt), func(t *testing.T) {
			db := openTestDB(t)
			sm := mustNewStateMachine(t, db, 11)
			task := fsmTestChannelMigrationTask("missing-task", "missing-channel")
			claim := fsmTestChannelMigrationClaim(task, 1, task.UpdatedAtMS+10000, task.UpdatedAtMS+1)
			cmds := make([]multiraft.Command, 256)
			lastToken := ""
			for i := range cmds {
				data := EncodeClaimChannelMigrationTaskCommand(claim)
				if i != staleAt {
					lastToken = fmt.Sprint(i)
					data = EncodeUpsertUserCommand(metadb.User{UID: "ordered-user", Token: lastToken})
				}
				cmds[i] = multiraft.Command{SlotID: 11, Index: uint64(i + 1), Term: 1, Data: data}
			}
			observer := &fsmProposalStageObserver{}
			ctx := multiraft.WithProposalStageObserver(context.Background(), observer)
			results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, cmds)
			if err != nil {
				t.Fatal(err)
			}
			for i, result := range results {
				want := ApplyResultOK
				if i == staleAt {
					want = ApplyResultStaleMeta
				}
				if string(result) != want {
					t.Fatalf("result[%d] = %q, want %q", i, result, want)
				}
			}
			user, err := db.ForSlot(11).GetUser(ctx, "ordered-user")
			if err != nil || user.Token != lastToken {
				t.Fatalf("ordered user = %+v, %v; want token %s", user, err, lastToken)
			}
			reopened := mustNewStateMachine(t, db, 11)
			if index, err := reopened.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx); err != nil || index != 256 {
				t.Fatalf("durable index = %d, %v; want 256", index, err)
			}
			commits := 0
			for _, event := range observer.events {
				if event.result == "ok" {
					commits++
				}
			}
			if commits > 16 {
				t.Fatalf("successful synchronous commits = %d, want <=16 for one stale entry in 256", commits)
			}
			t.Logf("256 commands, one stale: %d successful synchronous commits", commits)
		})
	}
}

func TestStaleBatchConditionalChainUsesCommittedPrefix(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 11)
	task := fsmTestChannelMigrationTask("chain-task", "chain-channel")
	claim := fsmTestChannelMigrationClaim(task, 1, task.UpdatedAtMS+10000, task.UpdatedAtMS+1)
	next := task
	next.Status, next.OwnerNodeID = claim.Status, claim.OwnerNodeID
	next.OwnerLeaseUntilMS, next.UpdatedAtMS = claim.OwnerLeaseUntilMS, claim.UpdatedAtMS
	renew := fsmTestChannelMigrationClaim(next, 1, task.UpdatedAtMS+20000, task.UpdatedAtMS+2)
	data := [][]byte{
		EncodeCreateChannelMigrationTaskCommand(task),
		EncodeClaimChannelMigrationTaskCommand(claim),
		EncodeClaimChannelMigrationTaskCommand(claim), // Old guard must fail.
		EncodeClaimChannelMigrationTaskCommand(renew),
	}
	cmds := make([]multiraft.Command, len(data))
	for i := range data {
		cmds[i] = multiraft.Command{SlotID: 11, Index: uint64(i + 1), Term: 1, Data: data[i]}
	}
	ctx := context.Background()
	results, err := sm.(multiraft.BatchStateMachine).ApplyBatch(ctx, cmds)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{ApplyResultOK, ApplyResultOK, ApplyResultStaleMeta, ApplyResultOK} {
		if string(results[i]) != want {
			t.Fatalf("result[%d] = %q, want %q", i, results[i], want)
		}
	}
	got, err := db.ForSlot(11).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	if err != nil || got.UpdatedAtMS != renew.UpdatedAtMS || got.OwnerLeaseUntilMS != renew.OwnerLeaseUntilMS {
		t.Fatalf("renewed task = %+v, %v; want final claim", got, err)
	}
	if index, err := sm.(multiraft.DurableAppliedStateMachine).DurableAppliedIndex(ctx); err != nil || index != 4 {
		t.Fatalf("durable index = %d, %v; want 4", index, err)
	}
}
