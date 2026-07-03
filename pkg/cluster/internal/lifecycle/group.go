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

// Start starts resources in order and stops already-started resources if any start fails.
func (g *Group) Start(ctx context.Context, resources ...NamedResource) error {
	for _, resource := range resources {
		if resource.Resource == nil {
			continue
		}
		if err := resource.Resource.Start(ctx); err != nil {
			_ = g.Stop(ctx)
			return err
		}
		g.started = append(g.started, resource)
	}
	return nil
}

// Stop stops resources in reverse start order and joins stop errors.
func (g *Group) Stop(ctx context.Context) error {
	var errs []error
	for i := len(g.started) - 1; i >= 0; i-- {
		resource := g.started[i]
		if resource.Resource == nil {
			continue
		}
		if err := resource.Resource.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	g.started = nil
	return errors.Join(errs...)
}
