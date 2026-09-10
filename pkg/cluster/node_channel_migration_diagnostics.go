package cluster

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// channelMigrationDiagnostics bounds operator-visible worker diagnostics to one
// entry per ten seconds. The node's serial migration loop owns this state.
type channelMigrationDiagnostics struct {
	logger wklog.Logger
	next   time.Time
}

func (d *channelMigrationDiagnostics) report(now time.Time, result channels.RepairScannerResult, executorErr, scannerErr error) {
	if d.logger == nil || now.Before(d.next) {
		return
	}
	if executorErr == nil && scannerErr == nil && result.TasksCreated == 0 && result.TasksAborted == 0 && len(result.Blocked) == 0 {
		return
	}
	d.next = now.Add(10 * time.Second)
	fields := []wklog.Field{
		wklog.Event("channel_migration.tick"),
		wklog.Int("pages_scanned", result.PagesScanned),
		wklog.Int("channels_scanned", result.ChannelsScanned),
		wklog.Int("tasks_created", result.TasksCreated),
		wklog.Int("tasks_aborted", result.TasksAborted),
		wklog.Int("blocked_observations", len(result.Blocked)),
	}
	if len(result.Blocked) > 0 {
		// These are bounded diagnostic examples, not a complete backlog census.
		fields = append(fields, wklog.Any("blocked_examples", result.Blocked[:min(4, len(result.Blocked))]))
	}
	if executorErr != nil {
		fields = append(fields, wklog.String("executor_error", boundedMigrationDiagnosticError(executorErr)))
	}
	if scannerErr != nil {
		fields = append(fields, wklog.String("scanner_error", boundedMigrationDiagnosticError(scannerErr)))
	}
	if executorErr != nil || scannerErr != nil {
		d.logger.Warn("channel migration worker tick failed", fields...)
		return
	}
	d.logger.Info("channel migration repair progress", fields...)
}

func boundedMigrationDiagnosticError(err error) string {
	const limit = 2048
	text := err.Error()
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
