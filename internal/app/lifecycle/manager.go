package lifecycle

import (
	"context"
	"errors"
	"sync"
)

// Manager starts and stops app runtime components as an ordered stack.
type Manager struct {
	mu         sync.Mutex
	components []Component
	started    []Component
}

// NewManager creates a Manager for components that must start in the provided order.
func NewManager(components ...Component) *Manager {
	return &Manager{components: append([]Component(nil), components...)}
}

// Start starts every registered component in order.
//
// If a component fails to start, Start stops only the components that already
// started, in reverse order, and returns the joined start and rollback errors.
func (m *Manager) Start(ctx context.Context) error {
	return m.StartWithRollbackContext(ctx, ctx)
}

// StartWithRollbackContext starts components with startCtx and rolls back with rollbackCtx on failure.
func (m *Manager) StartWithRollbackContext(startCtx, rollbackCtx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.started) > 0 {
		return nil
	}

	for _, component := range m.components {
		if err := component.Start(startCtx); err != nil {
			rollbackErrs := m.stopStartedLocked(rollbackCtx)
			return errors.Join(append([]error{err}, rollbackErrs...)...)
		}
		m.started = append(m.started, component)
	}
	return nil
}

// Stop stops currently started components in reverse start order.
//
// Stop is idempotent. It passes the caller context to every component stop and
// joins all stop errors in the order they occurred.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return errors.Join(m.stopStartedLocked(ctx)...)
}

func (m *Manager) stopStartedLocked(ctx context.Context) []error {
	started := m.started
	m.started = nil

	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		if err := started[i].Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
