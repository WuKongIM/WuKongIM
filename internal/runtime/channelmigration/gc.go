package channelmigration

import (
	"context"
	"time"
)

func (e *Executor) gcTerminalTasks(ctx context.Context, now time.Time) error {
	if e.cfg.GCRetention <= 0 || e.cfg.GCLimit <= 0 {
		return nil
	}
	beforeMS := now.Add(-e.cfg.GCRetention).UnixMilli()
	deleted, err := e.store.GarbageCollectTerminalTasks(ctx, beforeMS, e.cfg.GCLimit)
	if err != nil {
		return err
	}
	e.metrics.RecordGarbageCollection(deleted)
	return nil
}
