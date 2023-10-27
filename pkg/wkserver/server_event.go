package wkserver

import (
	"context"
	"errors"

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
	} else if msgType == proto.MsgTypeResp {
		resp := &proto.Response{}
		err := resp.Unmarshal(data)
		if err != nil {
			s.Error("unmarshal resp error", zap.Error(err))
			return
		}
		s.handleResp(conn, resp)
	} else if msgType == proto.MsgTypeMessage {
		msg := &proto.Message{}
		err := msg.Unmarshal(data)
		if err != nil {
			s.Error("unmarshal message error", zap.Error(err))
			return
		}
		s.handleMessage(conn, msg)
	} else {
		s.Error("unknown msg type", zap.Uint8("msgType", msgType.Uint8()))
	}
}

func (s *Server) handleHeartbeat(conn wknet.Conn) {

	_, err := conn.Write([]byte{proto.MsgTypeHeartbeat.Uint8()})
	if err != nil {
		s.Debug("write heartbeat error", zap.Error(err))
	}
}

func (s *Server) handleConnack(conn wknet.Conn, req *proto.Connect) {

	conn.SetUID(req.Uid)
	s.connManager.AddConn(req.Uid, conn)

	s.routeMapLock.RLock()
	h, ok := s.routeMap[s.opts.ConnPath]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", s.opts.ConnPath))
		return
	}
	ctx := NewContext(conn)
	ctx.connReq = req
	ctx.proto = s.proto
	h(ctx)
}

func (s *Server) handleResp(conn wknet.Conn, resp *proto.Response) {

	s.w.Trigger(resp.Id, resp)

}

func (s *Server) handleMessage(conn wknet.Conn, msg *proto.Message) {
	if s.opts.OnMessage != nil {
		s.opts.OnMessage(conn, msg)
	}
}

func (s *Server) handleRequest(conn wknet.Conn, req *proto.Request) {
	s.routeMapLock.RLock()
	h, ok := s.routeMap[req.Path]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", req.Path))
		return
	}
	ctx := NewContext(conn)
	ctx.req = req
	ctx.proto = s.proto
	h(ctx)
}

func (s *Server) Request(uid string, p string, body []byte) (*proto.Response, error) {
	conn := s.connManager.GetConn(uid)
	if conn == nil {
		return nil, errors.New("conn is nil")
	}
	r := &proto.Request{
		Id:   s.reqIDGen.Next(),
		Path: p,
		Body: body,
	}
	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}
	msgData, err := s.proto.Encode(data, proto.MsgTypeRequest.Uint8())
	if err != nil {
		return nil, err
	}
	ch := s.w.Register(r.Id)
	_, err = conn.WriteToOutboundBuffer(msgData)
	if err != nil {
		return nil, err
	}
	_ = conn.WakeWrite()
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.RequestTimeout)
	defer cancel()
	select {
	case x := <-ch:
		if x == nil {
			return nil, errors.New("unknown error")
		}
		return x.(*proto.Response), nil
	case <-timeoutCtx.Done():
		return nil, timeoutCtx.Err()
	}
}

func (s *Server) RequestAsync(uid string, p string, body []byte) error {
	conn := s.connManager.GetConn(uid)
	if conn == nil {
		return errors.New("conn is nil")
	}
	r := &proto.Request{
		Id:   s.reqIDGen.Next(),
		Path: p,
		Body: body,
	}
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	msgData, err := s.proto.Encode(data, proto.MsgTypeRequest.Uint8())
	if err != nil {
		return err
	}
	_, err = conn.WriteToOutboundBuffer(msgData)
	if err != nil {
		return err
	}
	return conn.WakeWrite()
}

func (s *Server) onConnect(conn wknet.Conn) error {

	return nil
}

func (s *Server) onClose(conn wknet.Conn) {
	s.connManager.RemoveConn(conn.UID())
	s.routeMapLock.RLock()
	h, ok := s.routeMap[s.opts.ClosePath]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", s.opts.ClosePath))
		return
	}

	ctx := NewContext(conn)
	ctx.proto = s.proto

	h(ctx)
}
