package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

const (
	defaultClusterWriteReadyTimeout = 10 * time.Second
	clusterWriteReadyPollInterval   = 10 * time.Millisecond
)

// clusterWriteReadyRuntime exposes the clusterv2 route state needed before gateway sends are admitted.
type clusterWriteReadyRuntime interface {
	Snapshot() clusterv2.Snapshot
	RouteHashSlot(uint16) (clusterv2.Route, error)
}

// Start starts the cluster first, then the gateway when configured.
func (a *App) Start(ctx context.Context) error {
	if a == nil {
		return ErrInvalidConfig
	}
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()

	if a.cluster == nil {
		return ErrInvalidConfig
	}
	if a.stopped {
		return ErrStopped
	}
	if a.started {
		return ErrAlreadyStarted
	}
	if err := a.cluster.Start(ctx); err != nil {
		return err
	}
	a.started = true
	a.clusterStarted = true
	if err := a.waitClusterWriteReady(ctx); err != nil {
		stopErr := a.cluster.Stop(ctx)
		if stopErr == nil {
			a.started = false
			a.clusterStarted = false
		}
		return errors.Join(err, stopErr)
	}
	if a.gateway != nil {
		if err := a.gateway.Start(); err != nil {
			stopErr := a.cluster.Stop(ctx)
			if stopErr == nil {
				a.started = false
				a.clusterStarted = false
			}
			return errors.Join(err, stopErr)
		}
		a.gatewayStarted = true
	}
	return nil
}

// Stop stops the gateway first, then the cluster.
func (a *App) Stop(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()

	a.stopped = true
	if !a.started {
		return nil
	}
	var err error
	if a.gatewayStarted && a.gateway != nil {
		if stopErr := a.gateway.Stop(); stopErr != nil {
			err = errors.Join(err, stopErr)
		} else {
			a.gatewayStarted = false
		}
	}
	if a.clusterStarted && a.cluster != nil {
		if stopErr := a.cluster.Stop(ctx); stopErr != nil {
			err = errors.Join(err, stopErr)
		} else {
			a.clusterStarted = false
		}
	}
	if !a.gatewayStarted && !a.clusterStarted {
		a.started = false
	}
	return err
}

func (a *App) waitClusterWriteReady(ctx context.Context) error {
	routes, ok := a.cluster.(clusterWriteReadyRuntime)
	if !ok {
		return nil
	}
	timeout := a.cfg.Cluster.Timeouts.Start
	if timeout <= 0 {
		timeout = defaultClusterWriteReadyTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(clusterWriteReadyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		if clusterWriteReady(routes, &lastErr) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("internalv2/app: cluster write readiness: %w", lastErr)
			}
			return fmt.Errorf("internalv2/app: cluster write readiness: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func clusterWriteReady(routes clusterWriteReadyRuntime, lastErr *error) bool {
	snapshot := routes.Snapshot()
	if !snapshot.RoutesReady || !snapshot.SlotsReady || !snapshot.ChannelsReady || snapshot.HashSlotCount == 0 {
		*lastErr = fmt.Errorf("snapshot not ready: routes=%t slots=%t channels=%t hashSlotCount=%d", snapshot.RoutesReady, snapshot.SlotsReady, snapshot.ChannelsReady, snapshot.HashSlotCount)
		return false
	}
	for hashSlot := uint16(0); hashSlot < snapshot.HashSlotCount; hashSlot++ {
		route, err := routes.RouteHashSlot(hashSlot)
		if err != nil {
			*lastErr = fmt.Errorf("route hash slot %d: %w", hashSlot, err)
			return false
		}
		if route.Leader == 0 {
			*lastErr = fmt.Errorf("route hash slot %d has no leader", hashSlot)
			return false
		}
	}
	return true
}
