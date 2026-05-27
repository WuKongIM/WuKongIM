package message

// Options configures the message usecase.
type Options struct {
	// Appender owns durable channel append routing.
	Appender Appender
	// MessageID allocates durable message ids.
	MessageID MessageIDAllocator
	// Authorizer decides whether a send may enter durable append.
	Authorizer Authorizer
	// Committed receives durable append events.
	Committed CommittedSink
	// Observer receives non-fatal send path observations.
	Observer Observer
}

// App orchestrates entry-agnostic message sends.
type App struct {
	appender   Appender
	messageID  MessageIDAllocator
	authorizer Authorizer
	committed  CommittedSink
	observer   Observer
}

// New creates a message App.
func New(opts Options) *App {
	if opts.Authorizer == nil {
		opts.Authorizer = allowAllAuthorizer{}
	}
	if opts.Committed == nil {
		opts.Committed = noopCommittedSink{}
	}
	return &App{
		appender:   opts.Appender,
		messageID:  opts.MessageID,
		authorizer: opts.Authorizer,
		committed:  opts.Committed,
		observer:   opts.Observer,
	}
}
