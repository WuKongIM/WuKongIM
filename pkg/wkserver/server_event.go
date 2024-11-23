package wkserver

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/pool/byteslice"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func (s *Server) onData(conn wknet.Conn) error {
	buff, err := conn.Peek(-1)
	if err != nil {
		return err
	}
	if len(buff) == 0 {
		return nil
	}

	newBuff := buff
	for len(newBuff) > 0 {
		data, msgType, size, err := s.proto.Decode(newBuff)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			break
		}
		newBuff = newBuff[size:]

		s.handleMsg(conn, msgType, data)
	}
	if len(newBuff) != len(buff) {
		_, err = conn.Discard(len(buff) - len(newBuff))
		if err != nil {
			s.Error("discard error", zap.Error(err))
		}
	}

	return nil
}

func (s *Server) handleMsg(conn wknet.Conn, msgType proto.MsgType, data []byte) {

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

		// 这需要复制一份新的body的byte，因为handleMsg传过来的data是被复用了的
		if len(req.Body) > 0 {
			newBody := make([]byte, len(req.Body)) // 这里内存有优化空间
			copy(newBody, req.Body)
			req.Body = newBody
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
		// 这需要复制一份新的body的byte，因为handleMsg传过来的data是被复用了的
		if len(resp.Body) > 0 {
			newBody := make([]byte, len(resp.Body))
			copy(newBody, resp.Body)
			resp.Body = newBody
		}

		s.handleResp(conn, resp)
	} else if msgType == proto.MsgTypeMessage {
		msg := &proto.Message{}
		err := msg.Unmarshal(data)
		if err != nil {
			s.Error("unmarshal message error", zap.Error(err))
			return
		}
		// 这需要复制一份新的content的byte，因为handleMsg传过来的data是被复用了的
		if len(msg.Content) > 0 {
			newContent := make([]byte, len(msg.Content))
			copy(newContent, msg.Content)
			msg.Content = newContent
		}
		if s.opts.MessagePoolOn {
			if s.messagePool.Running() > s.opts.MessagePoolSize-10 {
				s.Warn("message pool will full", zap.Int("running", s.messagePool.Running()), zap.Int("size", s.opts.MessagePoolSize))
			}
			err = s.messagePool.Submit(func(cn wknet.Conn, m *proto.Message) func() {
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
	//释放body
	byteslice.Put(r.Body)
}

func (s *Server) handleHeartbeat(conn wknet.Conn) {
	_, err := conn.WriteToOutboundBuffer([]byte{proto.MsgTypeHeartbeat.Uint8()})
	if err != nil {
		s.Debug("write heartbeat error", zap.Error(err))
	}
	err = conn.WakeWrite()
	if err != nil {
		s.Debug("wakeWrite error", zap.Error(err))
	}
}

func (s *Server) handleConnack(conn wknet.Conn, req *proto.Connect) {

	s.Debug("连接成功", zap.String("from", req.Uid))
	conn.SetUID(req.Uid)
	conn.SetMaxIdle(s.opts.MaxIdle)
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

func (s *Server) handleResp(_ wknet.Conn, resp *proto.Response) {
	if s.w.IsRegistered(resp.Id) {
		s.w.Trigger(resp.Id, resp)
	} else {
		s.Error("resp id not found", zap.Uint64("id", resp.Id))
	}
}

func (s *Server) handleMessage(conn wknet.Conn, msg *proto.Message) {
	if s.opts.OnMessage != nil {
		s.opts.OnMessage(conn, msg)
	}
}

func (s *Server) handleRequest(conn wknet.Conn, req *proto.Request) {
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
		s.Debug("request path", zap.String("path", req.Path), zap.Duration("cost", time.Since(start)), zap.String("from", conn.UID()))
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

func (s *Server) Send(uid string, msg *proto.Message) error {
	conn := s.connManager.GetConn(uid)
	if conn == nil {
		return errors.New("conn is nil")
	}
	data, err := msg.Marshal()
	if err != nil {
		return err
	}
	msgData, err := s.proto.Encode(data, proto.MsgTypeMessage.Uint8())
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
