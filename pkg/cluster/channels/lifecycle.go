package channels

import "context"

// Lifecycle wraps Service for lifecycle groups.
type Lifecycle struct {
	service *Service
}

// NewLifecycle creates a Lifecycle wrapper.
func NewLifecycle(service *Service) *Lifecycle { return &Lifecycle{service: service} }

// Start is a no-op because Channel starts during construction.
func (l *Lifecycle) Start(context.Context) error { return nil }

// Stop closes the Channel service.
func (l *Lifecycle) Stop(context.Context) error {
	if l == nil || l.service == nil {
		return nil
	}
	return l.service.Close()
}
