package delivery

// Options configures the delivery usecase.
type Options struct {
	// Runtime receives committed messages and delivery feedback.
	Runtime Runtime
}

// App orchestrates entry-agnostic online delivery.
type App struct {
	runtime Runtime
}

// New creates a delivery App.
func New(opts Options) *App {
	return &App{runtime: opts.Runtime}
}
