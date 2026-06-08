package message

// Options configures the message usecase.
type Options struct {
	// Appender owns durable channel append routing.
	Appender Appender
	// MessageReader owns compatible channel message sync reads.
	MessageReader ChannelMessageReader
	// MessageID allocates durable message ids.
	MessageID MessageIDAllocator
	// Authorizer decides whether a send may enter durable append.
	Authorizer Authorizer
	// Idempotency recovers successful sends after sender-authority response loss.
	Idempotency SendIdempotencyLookup
	// Committed receives durable append events.
	Committed CommittedSink
	// Observer receives non-fatal send path observations.
	Observer Observer
}

// App orchestrates entry-agnostic message sends.
type App struct {
	appender      Appender
	messageReader ChannelMessageReader
	messageID     MessageIDAllocator
	authorizer    Authorizer
	idempotency   SendIdempotencyLookup
	committed     CommittedSink
	observer      Observer
}

// New creates a message App.
func New(opts Options) *App {
	if opts.Authorizer == nil {
		opts.Authorizer = allowAllAuthorizer{}
	}
	return &App{
		appender:      opts.Appender,
		messageReader: opts.MessageReader,
		messageID:     opts.MessageID,
		authorizer:    opts.Authorizer,
		idempotency:   opts.Idempotency,
		committed:     opts.Committed,
		observer:      opts.Observer,
	}
}
