package server

type streamReactor struct {
	s    *Server
	subs []*streamReactorSub
}

func newStreamReactor(s *Server) *streamReactor {
	return &streamReactor{
		s: s,
	}
}

func (s *streamReactor) start() error {

	return nil
}

func (s *streamReactor) stop() {

}
