package logic

import (
	"fmt"
	"net"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/logicclient"
	"github.com/WuKongIM/WuKongIM/internal/monitor"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type Server struct {
	s    *wkserver.Server
	opts *options.Options
	wklog.Log
	logic          *Logic
	gatewayHandler *gatewayHandler
	writePool      *ants.Pool
}

func NewServer(addr string, opts *options.Options) *Server {

	s := &Server{
		opts: opts,
		s:    wkserver.New(addr),
		Log:  wklog.NewWKLog("Logic"),
	}
	monitor.SetMonitorOn(opts.Monitor.On) // 监控开关
	s.logic = NewLogic(opts)
	s.gatewayHandler = newGatewayHandler(s)

	writePool, err := ants.NewPool(opts.Cluster.WritePoolSize)
	if err != nil {
		s.Panic("create write pool error", zap.Error(err))
		return nil
	}
	s.writePool = writePool

	logicclient.SetLocalServer(s.logic) // 单机版

	return s
}

func (s *Server) Start() error {

	if !s.opts.ClusterOn() {
		cli := newLocalGatewayClient(s)

		s.logic.gatewayManager.add(cli)
	} else {
		cli := newRemoteGatewayClient(fmt.Sprintf("%d", s.opts.Cluster.NodeID), s)
		s.logic.gatewayManager.add(cli)
	}

	err := s.logic.Start()
	if err != nil {
		return err
	}

	err = s.s.Start()
	if err != nil {
		return err
	}
	s.gatewayHandler.initHandler()

	s.s.Route(s.s.Options().ConnPath, s.handleClientConn) // trigger when node connection

	s.s.Route(s.s.Options().ClosePath, s.handleClientClose) // trigger when node connection close
	return nil
}
func (s *Server) Stop() {

	// gatways := s.gatewayManager.getAllGateway()
	// for _, gatway := range gatways {
	// 	gatway.stop()
	// }
	s.s.Stop()

	s.logic.Stop()
}

func (s *Server) Addr() net.Addr {
	return s.s.Addr()
}

func (s *Server) handleClientConn(c *wkserver.Context) {
	req := c.ConnReq()

	conn := c.Conn()
	conn.SetUID(req.Uid)
	conn.SetAuthed(true)

	if !strings.Contains(req.Uid, "@") {
		s.Error("uid is error", zap.String("uid", req.Uid))
		c.WriteConnack(&proto.Connack{
			Id:     req.Id,
			Status: proto.Status_ERROR,
		})
		return
	}
	uidStrs := strings.Split(req.Uid, "@")
	role := uidStrs[1]
	if role == NodeRoleGateway.String() {
		gateway := s.logic.gatewayManager.get(req.Uid)
		if gateway == nil {
			gateway = newRemoteGatewayClient(req.Uid, s)
			s.logic.gatewayManager.add(gateway)
		}
		s.Debug("gateway connected", zap.String("gatewayID", c.Conn().UID()))
	}
	c.WriteConnack(&proto.Connack{
		Id:     req.Id,
		Status: proto.Status_OK,
	})
}

func (s *Server) handleClientClose(c *wkserver.Context) {

}
