package top

import (
	"context"
	"fmt"
	"io"
	"time"
)

// runInteractive refreshes top snapshots until bounded by config or context.
func runInteractive(ctx context.Context, w io.Writer, cfg config) error {
	refreshes := 0
	for {
		if refreshes > 0 && !cfg.JSON {
			fmt.Fprintln(w)
		}
		if err := runOnce(ctx, w, cfg); err != nil {
			return err
		}
		refreshes++
		if cfg.MaxRefresh > 0 && refreshes >= cfg.MaxRefresh {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cfg.Interval):
		}
	}
}
