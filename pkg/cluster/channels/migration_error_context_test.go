package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// A bounded tick must retain the task that failed before its deadline, so an
// operator can distinguish a failed replica probe from unrelated queued work.
func TestMigrationExecutorErrorIdentifiesTaskAndSurvivesTickCancellation(t *testing.T) {
	for _, cancelTick := range []bool{false, true} {
		t.Run(map[bool]string{false: "task_error", true: "tick_canceled"}[cancelTick], func(t *testing.T) {
			now := time.Unix(100, 0)
			task := testLeaderFailoverExecutorTask(ch.ChannelID{ID: "diagnostic-channel", Type: 2})
			task.TaskID = "diagnostic-task"
			task.OwnerNodeID = 2
			task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			meta := &migrationDiagnosticMeta{}
			if cancelTick {
				meta.cancel = cancel
			}
			executor := NewMigrationExecutor(MigrationExecutorConfig{LocalNode: 2, Source: &migrationRotatingSource{tasks: migrationFairSource{task}}, Store: &migrationFairStore{}, Runtime: &fakeMigrationExecutorRuntime{}, Meta: meta, Clock: func() time.Time { return now }, TaskLimit: 2})
			err := executor.RunOnce(ctx)
			require.ErrorIs(t, err, ch.ErrNotReady)
			require.ErrorContains(t, err, "diagnostic-task")
			require.ErrorContains(t, err, "diagnostic-channel")
			require.ErrorContains(t, err, "phase 1")
			if cancelTick {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}

type migrationDiagnosticMeta struct{ cancel context.CancelFunc }

func (m *migrationDiagnosticMeta) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	if m.cancel != nil {
		m.cancel()
	}
	return metadb.ChannelRuntimeMeta{}, ch.ErrNotReady
}
