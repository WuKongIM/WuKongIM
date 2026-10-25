package lifecycle

import (
	"context"
	"errors"
)

// Resource is a startable and stoppable runtime component.
type Resource interface {
	// Start starts the component.
	Start(context.Context) error
	// Stop stops the component.
	Stop(context.Context) error
}

// NamedResource labels a Resource for ordered lifecycle management.
type NamedResource struct {
	// Name identifies the resource in diagnostics.
	Name string
	// Resource is the managed component.
	Resource Resource
}

// Group starts resources in order and stops them in reverse order.
type Group struct {
	started []NamedResource
}

// HasStarted reports retained resources, including an unfinished rollback.
func (g *Group) HasStarted() bool { return len(g.started) != 0 }

// IsStopIncomplete recognizes ownership retained after a caller's stop budget.
// Ordinary cleanup errors do not prevent the remaining resources from stopping.
func IsStopIncomplete(err error) bool {
	var incomplete interface{ StopIncomplete() bool }
	return errors.As(err, &incomplete) && incomplete.StopIncomplete()
}

// Start starts resources in order and stops already-started resources if any start fails.
func (g *Group) Start(ctx context.Context, resources ...NamedResource) error {
	for _, resource := range resources {
		if resource.Resource == nil {
			continue
		}
		if err := resource.Resource.Start(ctx); err != nil {
			return errors.Join(err, g.Stop(ctx))
		}
		g.started = append(g.started, resource)
	}
	return nil
}

// Quiesce joins optional admission owners before any resource is destroyed.
// Start order lets an entry owner finish using later-started dependencies.
func (g *Group) Quiesce(ctx context.Context) error {
	var errs []error
	for _, resource := range g.started {
		owner, ok := resource.Resource.(interface{ Quiesce(context.Context) error })
		if !ok {
			continue
		}
		if err := owner.Quiesce(ctx); err != nil {
			errs = append(errs, err)
			if IsStopIncomplete(err) {
				return errors.Join(errs...)
			}
		}
	}
	return errors.Join(errs...)
}

// Stop stops resources in reverse start order and joins stop errors.
func (g *Group) Stop(ctx context.Context) error {
	var errs []error
	if err := g.Quiesce(ctx); err != nil {
		errs = append(errs, err)
		if IsStopIncomplete(err) {
			return errors.Join(errs...)
		}
	}
	for i := len(g.started) - 1; i >= 0; i-- {
		resource := g.started[i]
		if resource.Resource == nil {
			continue
		}
		if err := resource.Resource.Stop(ctx); err != nil {
			errs = append(errs, err)
			if IsStopIncomplete(err) {
				// Already-joined later resources are not stopped a second time.
				g.started = g.started[:i+1]
				return errors.Join(errs...)
			}
		}
	}
	g.started = nil
	return errors.Join(errs...)
}
