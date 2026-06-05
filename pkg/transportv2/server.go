package transportv2

// Server owns inbound connection and service registry state.
type Server struct {
	cfg ServerConfig
}

// NewServer validates config and builds a minimal server shell.
func NewServer(cfg ServerConfig) (*Server, error) {
	normalized, err := normalizeServerConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: normalized}, nil
}

// Stop releases server resources.
func (s *Server) Stop() {}

// Stats returns a point-in-time server stats snapshot.
func (s *Server) Stats() Stats { return Stats{} }
