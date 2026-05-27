package app

import (
	"context"
	"errors"
)

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
