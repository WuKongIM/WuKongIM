package wkserver

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func (s *Server) OnTraffic(c gnet.Conn) (action gnet.Action) {

	if s.opts.LogDetailOn {
		s.Info("OnTraffic...", zap.String("addr", c.RemoteAddr().String()))
	}
	if c.InboundBuffered() == 0 {
		return
	}
	batchCount := 0
	for i := 0; i < s.batchRead; i++ {
		data, msgType, _, err := s.proto.Decode(c)
		if err == io.ErrShortBuffer { // 表示数据不够了
			if s.opts.LogDetailOn {
				s.Info("err short buffer", zap.Error(err))
			}
			break
		}
		if err != nil {
			s.Foucs("decode error,gnet close", zap.Error(err))
			return gnet.Close
		}

		if s.opts.LogDetailOn {
			s.Info("OnTraffic.decode..", zap.Uint8("msgType", uint8(msgType)), zap.Int("data", len(data)))
		}

		batchCount++

		s.handleMsg(c, msgType, data)

	}
	if batchCount == s.batchRead && c.InboundBuffered() > 0 {
		if c.InboundBuffered() > 1024*1024 {
			s.Foucs("server: inbound buffered is too large", zap.Int("buffered", c.InboundBuffered()))
		}
		if err := c.Wake(nil); err != nil { // 这里调用wake避免丢失剩余的数据
			s.Foucs("failed to wake up the connection, gnet close", zap.Error(err))
			return gnet.Close
		}
	}

	return
}

func (s *Server) handleMsg(conn gnet.Conn, msgType proto.MsgType, data []byte) {

	s.metrics.recvMsgBytesAdd(uint64(len(data)))
	s.metrics.recvMsgCountAdd(1)

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

		req := s.requestObjPool.Get().(*proto.Request)
		err := req.Unmarshal(data)
		if err != nil {
			s.Error("unmarshal request error", zap.Error(err))
			return
		}

		if s.requestPool.Running() > s.opts.RequestPoolSize-10 {
			s.Warn("request pool will full", zap.Int("running", s.requestPool.Running()), zap.Int("size", s.opts.RequestPoolSize))
		}
		err = s.requestPool.Submit(func() {
			// handle request
			s.handleRequest(conn, req)

			// release request
			s.releaseRequest(req)
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
		// 这需要复制一份新的data的byte，因为handleMsg传过来的data是被复用了的

		msg := &proto.Message{}
		err := msg.Unmarshal(data)
		if err != nil {
			s.Error("unmarshal message error", zap.Error(err))
			return
		}
		if s.opts.MessagePoolOn {
			if s.messagePool.Running() > s.opts.MessagePoolSize-10 {
				s.Warn("message pool will full", zap.Int("running", s.messagePool.Running()), zap.Int("size", s.opts.MessagePoolSize))
			}
			err = s.messagePool.Submit(func(cn gnet.Conn, m *proto.Message) func() {
				return func() {
					s.handleMessage(cn, m)
				}

			}(conn, msg))
			if err != nil {
				s.Error("submit handleMessage error", zap.Error(err))
			}
		} else {
			s.handleMessage(conn, msg)
		}

	} else {
		s.Error("unknown msg type", zap.Uint8("msgType", msgType.Uint8()))
	}
}

func (s *Server) releaseRequest(r *proto.Request) {
	r.Reset()
	s.requestObjPool.Put(r)
}

func (s *Server) handleHeartbeat(conn gnet.Conn) {
	data, err := s.proto.Encode([]byte{proto.MsgTypeHeartbeat.Uint8()}, proto.MsgTypeHeartbeat)
	if err != nil {
		s.Error("encode heartbeat error", zap.Error(err))
		return
	}
	_, err = conn.Write(data)
	if err != nil {
		s.Debug("write heartbeat error", zap.Error(err))
	}
}

func (s *Server) handleConnack(conn gnet.Conn, req *proto.Connect) {

	s.Info("收到连接。。。", zap.String("from", req.Uid))
	conn.SetContext(newConnContext(req.Uid))
	s.connManager.AddConn(req.Uid, conn)

	s.routeMapLock.RLock()
	h, ok := s.routeMap[s.opts.ConnPath]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Info("route not found", zap.String("path", s.opts.ConnPath))
		return
	}
	ctx := NewContext(conn)
	ctx.connReq = req
	ctx.proto = s.proto
	h(ctx)
}

func (s *Server) handleResp(_ gnet.Conn, resp *proto.Response) {
	if s.w.IsRegistered(resp.Id) {
		s.w.Trigger(resp.Id, resp)
	} else {
		s.Error("resp id not found", zap.Uint64("id", resp.Id))
	}
}

func (s *Server) handleMessage(conn gnet.Conn, msg *proto.Message) {
	if s.opts.OnMessage != nil {
		s.opts.OnMessage(conn, msg)
	}
}

func (s *Server) handleRequest(conn gnet.Conn, req *proto.Request) {
	s.routeMapLock.RLock()
	handler, ok := s.routeMap[req.Path]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", req.Path))
		return
	}
	start := time.Now()
	ctx := NewContext(conn)
	ctx.req = req
	ctx.proto = s.proto
	handler(ctx)

	// 这里判断日志等级才调用debug，避免 time.Since(start)这些无谓的消耗
	if wklog.Level() == zapcore.DebugLevel {
		cost := time.Since(start)
		s.Debug("request path", zap.Uint64("id", req.Id), zap.String("path", req.Path), zap.Duration("cost", cost))
	}

}

func (s *Server) Request(uid string, p string, body []byte) (*proto.Response, error) {
	conn := s.connManager.GetConn(uid)
	if conn == nil {
		return nil, errors.New("conn is nil")
	}
	r := &proto.Request{
		Id:   s.reqIDGen.Inc(),
		Path: p,
		Body: body,
	}

	if s.opts.OnRequest != nil {
		s.opts.OnRequest(conn, r)
	}

	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}
	msgData, err := s.proto.Encode(data, proto.MsgTypeRequest)
	if err != nil {
		return nil, err
	}
	ch := s.w.Register(r.Id)
	err = conn.AsyncWrite(msgData, nil)
	if err != nil {
		return nil, err
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.RequestTimeout)
	defer cancel()
	select {
	case x := <-ch:
		if x == nil {
			return nil, errors.New("unknown error")
		}
		resp := x.(*proto.Response)
		if s.opts.OnResponse != nil {
			s.opts.OnResponse(conn, resp)
		}
		return resp, nil
	case <-timeoutCtx.Done():
		s.w.Trigger(r.Id, nil)
		return nil, timeoutCtx.Err()
	}
}

func (s *Server) RequestAsync(uid string, p string, body []byte) error {
	conn := s.connManager.GetConn(uid)
	if conn == nil {
		return errors.New("conn is nil")
	}
	r := &proto.Request{
		Id:   s.reqIDGen.Inc(),
		Path: p,
		Body: body,
	}
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	msgData, err := s.proto.Encode(data, proto.MsgTypeRequest)
	if err != nil {
		return err
	}
	err = conn.AsyncWrite(msgData, nil)
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) Send(uid string, msg *proto.Message) error {
	conn := s.connManager.GetConn(uid)
	if conn == nil {
		return errors.New("conn is nil")
	}
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	msgData, err := s.proto.Encode(data, proto.MsgTypeMessage)
	if err != nil {
		return err
	}
	err = conn.AsyncWrite(msgData, nil)
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) OnBoot(eng gnet.Engine) (action gnet.Action) {
	s.engine = eng
	return
}

func (s *Server) OnClose(conn gnet.Conn, err error) (action gnet.Action) {

	ctx := conn.Context()
	if ctx == nil {
		return
	}
	connCtx := ctx.(*connContext)

	s.connManager.RemoveConn(connCtx.uid.Load())
	s.routeMapLock.RLock()
	h, ok := s.routeMap[s.opts.ClosePath]
	s.routeMapLock.RUnlock()
	if !ok {
		s.Debug("route not found", zap.String("path", s.opts.ClosePath))
		return
	}

	ct := NewContext(conn)
	ct.proto = s.proto

	h(ct)

	return
}

func (s *Server) OnTick() (delay time.Duration, action gnet.Action) {
	delay = time.Millisecond * 100
	return
}
