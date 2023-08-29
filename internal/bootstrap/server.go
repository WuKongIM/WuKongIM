package server

import (
	"github.com/WuKongIM/WuKongIM/internal/gateway"
	"github.com/WuKongIM/WuKongIM/internal/options"
)

type Server struct {
	gateway *gateway.Gateway
	opts    *options.Options
}

func New(opts *options.Options) *Server {
	s := &Server{
		opts: opts,
	}
	s.gateway = gateway.New(options.New())
	return s
}

func (s *Server) Start() error {

	err := s.gateway.Start()
	if err != nil {
		return err
	}
	return nil
}
