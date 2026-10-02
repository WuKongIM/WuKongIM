package mqttsession

import (
	"context"
	"errors"
	"sync"
	"time"

	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

var (
	ErrOwnerSweeperInvalid  = errors.New("mqttsession: invalid owner sweeper configuration")
	ErrOwnerSweeperStopping = errors.New("mqttsession: owner sweeper still draining")
)

// OwnerSweeperOptions bounds cleanup independently of connection registration.
type OwnerSweeperOptions struct {
	Owners   *Owners
	Registry *gr.Registry
	// Limit defaults to 256 due owners per turn and cannot exceed 256.
	Limit int
	// Interval defaults to 250ms; it is bounded between 1ms and one minute.
	Interval time.Duration
	// TurnTimeout defaults to 250ms, bounded between 1ms and five seconds.
	// A slow callback still owns the turn until it returns; it is never detached.
	TurnTimeout time.Duration
	// Observe receives aggregate snapshots synchronously after each turn.
	Observe func(OwnerSweepObservation)
}

// OwnerSweepObservation counts attempts, not unique retirements or proof receipts.
type OwnerSweepObservation struct {
	Visited, Failures int
	Owners            OwnerSnapshot
}

// OwnerSweeper drives the existing indexed heap in one joined loop. It does not
// transition durable Sessions, renew leases or discard unresolved effects.
type OwnerSweeper struct {
	opts   OwnerSweeperOptions
	mu     sync.Mutex
	run    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func NewOwnerSweeper(o OwnerSweeperOptions) (*OwnerSweeper, error) {
	if o.Limit == 0 {
		o.Limit = 256
	}
	if o.Interval == 0 {
		o.Interval = 250 * time.Millisecond
	}
	if o.TurnTimeout == 0 {
		o.TurnTimeout = 250 * time.Millisecond
	}
	if o.Owners == nil || o.Limit < 1 || o.Limit > 256 || o.Interval < time.Millisecond || o.Interval > time.Minute || o.TurnTimeout < time.Millisecond || o.TurnTimeout > 5*time.Second {
		return nil, ErrOwnerSweeperInvalid
	}
	return &OwnerSweeper{opts: o}, nil
}

// Start uses the context only for admission; repeated calls share the same run.
// A timed-out Stop must finish joining before another run may start.
func (w *OwnerSweeper) Start(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrOwnerSweeperInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		if w.run.Err() != nil {
			return ErrOwnerSweeperStopping
		}
		return nil
	}
	run, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w.run, w.cancel, w.done = run, cancel, done
	gr.SafeGo(w.opts.Registry, gr.TaskMQTTOwnerSweeper, func() { defer close(done); w.loop(run) })
	return nil
}

// Stop cancels and joins this run, retaining it on timeout. It does not close
// owner admission or erase retained owners; app owns final registry shutdown.
func (w *OwnerSweeper) Stop(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrOwnerSweeperInvalid
	}
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	if cancel != nil {
		cancel()
	}
	w.mu.Unlock()
	if cancel == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	w.mu.Lock()
	if w.done == done {
		w.run, w.cancel, w.done = nil, nil, nil
	}
	w.mu.Unlock()
	return nil
}

func (w *OwnerSweeper) loop(ctx context.Context) {
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		o := w.sweep(ctx)
		if w.opts.Observe != nil {
			w.opts.Observe(o)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweep delegates selection and fencing to Owners under its existing lock.
// Failed closes move behind other due owners and retain their cleanup deadline.
func (w *OwnerSweeper) sweep(parent context.Context) (out OwnerSweepObservation) {
	ctx, cancel := context.WithTimeout(parent, w.opts.TurnTimeout)
	defer cancel()
	var err error
	out.Visited, err = w.opts.Owners.Sweep(ctx, w.opts.Limit)
	if err != nil || ctx.Err() != nil {
		out.Failures = 1
	}
	out.Owners = w.opts.Owners.Snapshot()
	return out
}
