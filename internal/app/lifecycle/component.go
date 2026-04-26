package lifecycle

import "context"

// Component is a startable/stoppable app runtime unit managed by Manager.
type Component interface {
	// Name returns a stable human-readable component name for diagnostics.
	Name() string
	// Start brings the component online.
	Start(context.Context) error
	// Stop releases the component's runtime resources.
	Stop(context.Context) error
}
