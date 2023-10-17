package multiraft

type Server struct {
	options *Options
}

func New(opts ...Option) *Server {
	defaultOptions := NewOptions()
	for _, opt := range opts {
		opt(defaultOptions)
	}
	return &Server{
		options: defaultOptions,
	}
}

func (s *Server) Start() error {

	return nil
}

func (s *Server) Stop() {
}
