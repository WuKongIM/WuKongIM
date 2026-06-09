package message

// Options configures the message usecase.
type Options struct {
	// Submitter owns channel-authority send routing and append admission.
	Submitter Submitter
	// Reader owns compatible channel message sync reads.
	Reader ChannelMessageReader
}

// App is a thin message facade over channel write submission and sync reads.
type App struct {
	submitter Submitter
	reader    ChannelMessageReader
}

// New creates a message App.
func New(opts Options) *App {
	return &App{
		submitter: opts.Submitter,
		reader:    opts.Reader,
	}
}
