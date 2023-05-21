package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type MonitorServer struct {
	s *Server
	r *wkhttp.WKHttp
	wklog.Log
	addr string
}

func NewMonitorServer(s *Server) *MonitorServer {
	r := wkhttp.New()
	r.Use(wkhttp.CORSMiddleware())
	return &MonitorServer{
		addr: s.opts.Monitor.Addr,
		s:    s,
		r:    r,
		Log:  wklog.NewWKLog("MonitorServer"),
	}

}

func (m *MonitorServer) Start() {
	// 监控api
	monitorapi := NewMonitorAPI(m.s)
	monitorapi.Route(m.r)

	go func() {
		err := m.r.Run(m.addr) // listen and serve
		if err != nil {
			panic(err)
		}
	}()
	m.Info("MonitorServer started", zap.String("addr", m.addr))
}

func (m *MonitorServer) Stop() error {

	return nil
}
