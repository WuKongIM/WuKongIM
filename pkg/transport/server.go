package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type handlerTable struct {
	handlers map[uint8]MessageHandler
}

func newHandlerTable() *handlerTable {
	return &handlerTable{handlers: make(map[uint8]MessageHandler)}
}

func (t *handlerTable) clone() *handlerTable {
	copied := make(map[uint8]MessageHandler, len(t.handlers))
	for msgType, handler := range t.handlers {
		copied[msgType] = handler
	}
	return &handlerTable{handlers: copied}
}

func (t *handlerTable) get(msgType uint8) MessageHandler {
	if t == nil {
		return nil
	}
	return t.handlers[msgType]
}

type rpcHandlerHolder struct {
	handler RPCHandler
}

type ServerConfig struct {
	ConnConfig ConnConfig
	Logger     wklog.Logger
}

// Server accepts inbound connections and dispatches messages by msgType.
type Server struct {
	listener   net.Listener
	handlers   atomic.Pointer[handlerTable]
	rpcHandler atomic.Pointer[rpcHandlerHolder]
	conns      sync.Map
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	cfg        ServerConfig
}

func NewServer() *Server {
	return NewServerWithConfig(ServerConfig{})
}

func NewServerWithConfig(cfg ServerConfig) *Server {
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	s := &Server{stopCh: make(chan struct{}), cfg: cfg}
	s.handlers.Store(newHandlerTable())
	return s
}

func (s *Server) Handle(msgType uint8, h MessageHandler) {
	if msgType == 0 || msgType == MsgTypeRPCRequest || msgType == MsgTypeRPCResponse {
		panic("nodetransport: reserved message type")
	}
	for {
		current := s.handlers.Load()
		next := current.clone()
		next.handlers[msgType] = h
		if s.handlers.CompareAndSwap(current, next) {
			return
		}
	}
}

func (s *Server) HandleRPC(h RPCHandler) {
	s.rpcHandler.Store(&rpcHandlerHolder{handler: h})
}

func (s *Server) HandleRPCMux(mux *RPCMux) {
	if mux == nil {
		panic("nodetransport: nil rpc mux")
	}
	s.HandleRPC(mux.HandleRPC)
}

func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.conns.Range(func(key, _ any) bool {
			key.(*MuxConn).Close()
			return true
		})
		s.wg.Wait()
	})
}

func (s *Server) Listener() net.Listener {
	return s.listener
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		raw, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				continue
			}
		}
		setTCPKeepAlive(raw)
		s.wg.Add(1)
		go s.serveConn(raw)
	}
}

func (s *Server) serveConn(raw net.Conn) {
	defer s.wg.Done()

	var mc *MuxConn
	dispatch := func(msgType uint8, body []byte, release func()) {
		s.dispatch(mc, msgType, body, release)
	}
	mc = newMuxConn(raw, dispatch, s.cfg.ConnConfig)
	s.conns.Store(mc, struct{}{})
	defer func() {
		s.conns.Delete(mc)
		mc.Close()
	}()

	select {
	case <-mc.readerDone:
	case <-s.stopCh:
	}
}

func (s *Server) dispatch(mc *MuxConn, msgType uint8, body []byte, release func()) {
	switch msgType {
	case MsgTypeRPCRequest:
		holder := s.rpcHandler.Load()
		if holder == nil || holder.handler == nil {
			release()
			return
		}
		copied := append([]byte(nil), body...)
		release()
		s.wg.Add(1)
		go s.handleRPCRequest(mc, holder.handler, copied)
	default:
		h := s.handlers.Load().get(msgType)
		if h != nil {
			h(body)
		}
		release()
	}
}

func (s *Server) handleRPCRequest(mc *MuxConn, handler RPCHandler, body []byte) {
	defer s.wg.Done()
	if len(body) < 8 {
		return
	}
	requestID := binary.BigEndian.Uint64(body[0:8])
	payload := body[8:]

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		select {
		case <-mc.readerDone:
		case <-s.stopCh:
		case <-done:
			return
		}
		cancel()
	}()

	respData, err := handler(ctx, payload)
	close(done)
	cancel()
	var errCode uint8
	if err != nil {
		errCode = 1
		respData = []byte(err.Error())
	}
	respBody := encodeRPCResponse(requestID, errCode, respData)
	if sendErr := mc.Send(PriorityRPC, MsgTypeRPCResponse, respBody); sendErr != nil {
		s.cfg.Logger.Warn("rpc response send failed",
			wklog.Uint64("requestID", requestID),
			wklog.Error(sendErr),
		)
	}
}

func encodeRPCRequest(requestID uint64, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint64(buf[0:8], requestID)
	copy(buf[8:], payload)
	return buf
}

func encodeRPCResponse(requestID uint64, errCode uint8, data []byte) []byte {
	buf := make([]byte, 9+len(data))
	binary.BigEndian.PutUint64(buf[0:8], requestID)
	buf[8] = errCode
	copy(buf[9:], data)
	return buf
}

func decodeRPCResponse(body []byte) (requestID uint64, errCode uint8, data []byte, err error) {
	if len(body) < 9 {
		return 0, 0, nil, fmt.Errorf("nodetransport: rpc response body too short: %d", len(body))
	}
	requestID = binary.BigEndian.Uint64(body[0:8])
	errCode = body[8]
	data = body[9:]
	return requestID, errCode, data, nil
}
