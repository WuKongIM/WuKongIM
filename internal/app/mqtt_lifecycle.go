package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
)

const mqttGenerationHandlerKey = "app.mqtt.generation_handler"

// mqttAcceptedGeneration keeps handshake cleanup alive through Gateway reply
// admission. Open, rollback and close can all finish the same handoff exactly once.
type mqttAcceptedGeneration struct {
	handler *access.Handler
	finish  func()
}

// mqttProduct is the stable Gateway/RPC entry across restore generations. mu
// serializes lifecycle transitions; packet callbacks and wakes never take it.
type mqttProduct struct {
	mu sync.Mutex
	// current publishes only a completely started generation. Old callbacks pin
	// their original handler and cannot resolve through this pointer again.
	current   atomic.Pointer[mqttGeneration]
	admitting atomic.Bool
	// pending retains partial construction/start until cleanup has joined.
	pending            *mqttGeneration
	build              func(context.Context) (*mqttGeneration, error)
	suspended, stopped bool
}

// Start precedes Gateway admission. Maintenance may already have terminally
// stopped the constructor's generation; only Resume may replace that generation.
func (m *mqttProduct) Start(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrStopped
	}
	if m.suspended || m.current.Load() != nil {
		return nil
	}
	if m.pending == nil {
		return ErrInvalidConfig
	}
	if err := m.pending.Start(ctx); err != nil {
		return err
	}
	m.current.Store(m.pending)
	m.pending = nil
	m.admitting.Store(true)
	return nil
}

// Suspend closes MQTT admission before joining every producer and Owner. A
// failed join retains its generation, dependencies and unknown-effect barriers.
func (m *mqttProduct) Suspend(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suspended = true
	m.admitting.Store(false)
	if current := m.current.Load(); current != nil {
		current.restoring.Store(true)
	}
	if m.pending != nil {
		m.pending.restoring.Store(true)
	}
	return m.joinLocked(ctx)
}

// Resume reconstructs terminal runtimes only after proved retirement. The
// public Gateway stays in maintenance until the cluster's restore fence clears.
func (m *mqttProduct) Resume(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return ErrStopped
	}
	if !m.suspended {
		return nil
	}
	if err := m.joinLocked(ctx); err != nil {
		return err
	}
	if m.build == nil {
		return ErrInvalidConfig
	}
	var err error
	m.pending, err = m.build(ctx)
	if err != nil {
		return err
	}
	if err = m.pending.Start(ctx); err != nil {
		return errors.Join(err, m.joinLocked(ctx))
	}
	if err = ctx.Err(); err != nil {
		return errors.Join(err, m.joinLocked(ctx))
	}
	m.current.Store(m.pending)
	m.pending = nil
	m.suspended = false
	m.admitting.Store(true)
	return nil
}

// Stop is terminal for the product, unlike restore suspension. Concurrent or
// late restore resume can never reopen a shutting-down application's MQTT entry.
func (m *mqttProduct) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	m.admitting.Store(false)
	return m.joinLocked(ctx)
}

func (m *mqttProduct) joinLocked(ctx context.Context) error {
	var result error
	if current := m.current.Load(); current != nil {
		result = current.Stop(ctx)
	}
	if m.pending != nil {
		if err := m.pending.Stop(ctx); err != nil {
			result = errors.Join(result, err)
		} else {
			m.pending = nil
		}
	}
	return result
}

// Quiesce serves the existing exact-owner RPC through the currently published
// registry and node-local persisted facts. It supplies no partition proof.
func (m *mqttProduct) Quiesce(ctx context.Context, owner contract.Owner) error {
	if m == nil {
		return runtime.ErrOwnerUnknown
	}
	g := m.current.Load()
	if g == nil {
		return runtime.ErrOwnerUnknown
	}
	return (runtime.Isolation{Owners: g.owners, Retired: g.retirements}).Quiesce(ctx, owner)
}

func (m *mqttProduct) wakeSource(source string) {
	if m == nil {
		return
	}
	if g := m.current.Load(); g != nil && g.deliveries != nil {
		_ = g.deliveries.WakeSource(source)
	}
}

// OnConnect selects one generation and transfers its handler with the accepted
// connection. Every later callback stays bound to that exact composition.
func (m *mqttProduct) OnConnect(ctx gt.Context, packet any) (*gt.PacketAuthResult, error) {
	if m == nil || !m.admitting.Load() {
		return nil, access.ErrHandlerClosed
	}
	g := m.current.Load()
	if g == nil {
		return nil, access.ErrHandlerClosed
	}
	request, finish, err := g.beginAuthentication(ctx.RequestContext)
	if err != nil {
		return nil, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			finish()
		}
	}()
	ctx.RequestContext = request
	result, err := g.handler.OnConnect(ctx, packet)
	if err == nil && result != nil && result.Accepted {
		if result.SessionValues == nil {
			result.SessionValues = make(map[string]any)
		}
		result.SessionValues[mqttGenerationHandlerKey] = &mqttAcceptedGeneration{handler: g.handler, finish: finish}
		rollback := result.Rollback
		result.Rollback = func(err error) {
			defer finish()
			if rollback != nil {
				rollback(err)
			}
		}
		handedOff = true
	}
	return result, err
}

// beginAuthentication accounts for the complete acquisition callback, including
// authentication and exact-owner isolation before the first reservation.
func (g *mqttGeneration) beginAuthentication(parent context.Context) (context.Context, func(), error) {
	if parent == nil {
		return nil, nil, access.ErrHandlerClosed
	}
	g.authMu.Lock()
	if g.authStopped {
		g.authMu.Unlock()
		return nil, nil, access.ErrHandlerClosed
	}
	if g.authCalls == 0 {
		g.authDone = make(chan struct{})
	}
	g.authCalls++
	g.authMu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(g.authContext, cancel)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			stop()
			cancel()
			g.authMu.Lock()
			g.authCalls--
			if g.authCalls == 0 {
				close(g.authDone)
			}
			g.authMu.Unlock()
		})
	}, nil
}

func (g *mqttGeneration) stopAuthentication(ctx context.Context) error {
	g.authMu.Lock()
	g.authStopped = true
	if g.cancelAuth != nil {
		g.cancelAuth()
	}
	done, active := g.authDone, g.authCalls
	g.authMu.Unlock()
	if active == 0 {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func mqttBoundGeneration(ctx gt.Context) *mqttAcceptedGeneration {
	if ctx.Session == nil {
		return nil
	}
	g, _ := ctx.Session.Value(mqttGenerationHandlerKey).(*mqttAcceptedGeneration)
	return g
}

func (m *mqttProduct) OnSessionOpen(ctx gt.Context) error {
	if g := mqttBoundGeneration(ctx); g != nil {
		defer g.finish()
		return g.handler.OnSessionOpen(ctx)
	}
	return access.ErrHandlerClosed
}
func (m *mqttProduct) OnPacket(ctx gt.Context, packet any) error {
	if g := mqttBoundGeneration(ctx); g != nil {
		return g.handler.OnPacket(ctx, packet)
	}
	return access.ErrHandlerClosed
}
func (m *mqttProduct) OnSessionClose(ctx gt.Context) error {
	if g := mqttBoundGeneration(ctx); g != nil {
		defer g.finish()
		return g.handler.OnSessionClose(ctx)
	}
	return nil
}
func (m *mqttProduct) OnSessionError(ctx gt.Context, err error) {
	if g := mqttBoundGeneration(ctx); g != nil {
		g.handler.OnSessionError(ctx, err)
	}
}
func (*mqttProduct) OnListenerError(string, error) {}

var _ gt.PacketHandler = (*mqttProduct)(nil)
