package wkserver

import (
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

func (s *Server) onData(conn wknet.Conn) error {
	buff, err := conn.Peek(-1)
	if err != nil {
		return err
	}
	if len(buff) == 0 {
		return nil
	}
	data, msgType, size, err := s.proto.Decode(buff)
	if err != nil {
		return err
	}
	if size > 0 {
		_, err = conn.Discard(size)
		if err != nil {
			s.Error("discard error", zap.Error(err))
		}
	}
	s.handleMsg(conn, msgType, data)

	return nil
}

func (s *Server) handleMsg(conn wknet.Conn, msgType proto.MsgType, data []byte) {
	if msgType == proto.MsgTypeHeartbeat {
		s.handleHeartbeat(conn)
	} else if msgType == proto.MsgTypeConnect {
		req := &proto.Connect{}
		err := req.Unmarshal(data)
		if err != nil {
			s.Error("unmarshal connack error", zap.Error(err))
			return
		}
		s.handleConnack(conn, req)
	} else if msgType == proto.MsgTypeRequest {
		req := &proto.Request{}
		err := req.Unmarshal(data)
		if err != nil {
			s.Error("unmarshal request error", zap.Error(err))
			return
		}
		err = s.requestPool.Submit(func() {
			s.handleRequest(conn, req)
		})
		if err != nil {
			s.Error("submit request error", zap.Error(err))
		}
	}
}

func (s *Server) handleHeartbeat(conn wknet.Conn) {

	_, err := conn.Write([]byte{proto.MsgTypeHeartbeat.Uint8()})
	if err != nil {
		s.Debug("write heartbeat error", zap.Error(err))
	}
}

func (s *Server) handleConnack(conn wknet.Conn, req *proto.Connect) {
	s.routeMapLock.RLock()
	h, ok := s.routeMap[s.opts.ConnPath]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", s.opts.ConnPath))
		return
	}
	ctx := &Context{
		conn:    conn,
		proto:   s.proto,
		connReq: req,
	}
	h(ctx)
}

func (s *Server) handleRequest(conn wknet.Conn, req *proto.Request) {
	s.routeMapLock.RLock()
	h, ok := s.routeMap[req.Path]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", req.Path))
		return
	}
	ctx := &Context{
		conn:  conn,
		req:   req,
		proto: s.proto,
	}
	h(ctx)
}

func (s *Server) onConnect(conn wknet.Conn) error {

	return nil
}

func (s *Server) onClose(conn wknet.Conn) {
	s.routeMapLock.RLock()
	h, ok := s.routeMap[s.opts.ClosePath]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", s.opts.ClosePath))
		return
	}
	ctx := &Context{
		conn:  conn,
		proto: s.proto,
	}
	h(ctx)
}
