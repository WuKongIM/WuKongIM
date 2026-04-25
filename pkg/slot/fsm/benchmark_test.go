package fsm

import (
	"context"
	"fmt"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

func BenchmarkApplySingle(b *testing.B) {
	db := openTestDB(b)
	sm := mustNewStateMachine(b, db, 1)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		user := metadb.User{
			UID:         fmt.Sprintf("bench-u-%d", i%64),
			Token:       fmt.Sprintf("tok-%d", i),
			DeviceFlag:  int64(i % 8),
			DeviceLevel: int64(i % 16),
		}
		if _, err := sm.Apply(ctx, multiraft.Command{
			SlotID: 1,
			Index:  uint64(i + 1),
			Term:   1,
			Data:   EncodeUpsertUserCommand(user),
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkApplyBatch10(b *testing.B) {
	benchmarkApplyBatch(b, 10)
}

func BenchmarkApplyBatch100(b *testing.B) {
	benchmarkApplyBatch(b, 100)
}

func benchmarkApplyBatch(b *testing.B, batchSize int) {
	db := openTestDB(b)
	sm := mustNewStateMachine(b, db, 1).(multiraft.BatchStateMachine)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmds := make([]multiraft.Command, batchSize)
		for j := 0; j < batchSize; j++ {
			idx := i*batchSize + j
			user := metadb.User{
				UID:         fmt.Sprintf("bench-u-%d", idx%64),
				Token:       fmt.Sprintf("tok-%d", idx),
				DeviceFlag:  int64(idx % 8),
				DeviceLevel: int64(idx % 16),
			}
			cmds[j] = multiraft.Command{
				SlotID: 1,
				Index:  uint64(idx + 1),
				Term:   1,
				Data:   EncodeUpsertUserCommand(user),
			}
		}
		if _, err := sm.ApplyBatch(ctx, cmds); err != nil {
			b.Fatal(err)
		}
	}
}
