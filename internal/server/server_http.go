package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

// APIServer ApiServer
type APIServer struct {
	r    *wkhttp.WKHttp
	addr string
	s    *Server
	wklog.Log
}

// NewAPIServer new一个api server
func NewAPIServer(s *Server) *APIServer {
	r := wkhttp.New()
	r.Use(wkhttp.CORSMiddleware())

	hs := &APIServer{
		r:    r,
		addr: s.opts.HTTPAddr,
		s:    s,
		Log:  wklog.NewWKLog("APIServer"),
	}
	return hs
}

// Start 开始
func (s *APIServer) Start() {
	s.setRoutes()
	go func() {
		err := s.r.Run(s.addr) // listen and serve
		if err != nil {
			panic(err)
		}
	}()
	s.Info("服务开启", zap.String("addr", s.s.opts.HTTPAddr))
}

// Stop 停止服务
func (s *APIServer) Stop() {
}

func (s *APIServer) setRoutes() {
	connz := NewConnzAPI(s.s)
	connz.Route(s.r)

	varz := NewVarzAPI(s.s)
	varz.Route(s.r)
}
