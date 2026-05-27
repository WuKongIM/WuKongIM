package app

import (
	"context"
	"errors"
)

// Start starts the cluster first, then the gateway when configured.
func (a *App) Start(ctx context.Context) error {
	if a == nil || a.cluster == nil {
		return ErrInvalidConfig
	}
	if a.stopped.Load() {
		return ErrStopped
	}
	if !a.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	if err := a.cluster.Start(ctx); err != nil {
		a.started.Store(false)
		return err
	}
	if a.gateway != nil {
		if err := a.gateway.Start(); err != nil {
			stopErr := a.cluster.Stop(ctx)
			a.started.Store(false)
			return errors.Join(err, stopErr)
		}
	}
	return nil
}

// Stop stops the gateway first, then the cluster.
func (a *App) Stop(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.stopped.Store(true)
	if !a.started.Swap(false) {
		return nil
	}
	var err error
	if a.gateway != nil {
		err = errors.Join(err, a.gateway.Stop())
	}
	if a.cluster != nil {
		err = errors.Join(err, a.cluster.Stop(ctx))
	}
	return err
}
