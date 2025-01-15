package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/panjf2000/ants/v2"
	"github.com/panjf2000/gnet/v2"
	"go.etcd.io/etcd/pkg/v3/wait"
	"go.uber.org/zap"
)

type clientStats struct {
	Requesting atomic.Int64 // 请求中的数量
}
type Client struct {
	clientStats
	cli *gnet.Client
	wklog.Log
	eng gnet.Engine

	opts  *Options
	event *clientEvent

	reqIDGen atomic.Uint64 // 日志id生成

	conns []*conn

	cancelCtx context.Context
	cancel    context.CancelFunc

	routeMapLock sync.RWMutex
	routeMap     map[string]Handler

	proto proto.Protocol
	pool  *ants.Pool
	w     wait.Wait

	batchRead int // 连接进来数据后，每次数据读取批数，超过此次数后下次再读
}

func New(addr string, opt ...Option) *Client {

	opts := NewOptions()
	opts.Addr = addr
	for _, o := range opt {
		o(opts)
	}

	c := &Client{
		Log:       wklog.NewWKLog(fmt.Sprintf("Client[%s]", addr)),
		opts:      opts,
		routeMap:  make(map[string]Handler),
		proto:     proto.New(),
		w:         wait.New(),
		batchRead: 1000,
	}

	c.cancelCtx, c.cancel = context.WithCancel(context.Background())
	var err error
	c.pool, err = ants.NewPool(1024*10, ants.WithNonblocking(true))
	if err != nil {
		c.Panic("new pool failed", zap.Error(err), zap.Stack("stack"))
	}

	c.event = &clientEvent{
		c: c,
	}

	c.conns = make([]*conn, 1)
	c.conns[0] = newConn(addr, c) // 目前只支持一个连接

	gc, err := gnet.NewClient(c.event, gnet.WithTicker(true), gnet.WithMulticore(true))
	if err != nil {
		c.Panic("new client failed", zap.Error(err))
		return nil
	}
	c.cli = gc

	return c
}

func (c *Client) Options() *Options {
	return c.opts
}

func (c *Client) Start() error {
	return c.cli.Start()
}

func (c *Client) Stop() {
	c.Foucs("client stop...")
	c.cancel()
	err := c.cli.Stop()
	if err != nil {
		c.Error("client stop failed", zap.Error(err))
	}
}

func (c *Client) Write(data []byte) error {
	return c.conn().asyncWrite(data)
}

func (c *Client) Route(p string, h Handler) {
	c.routeMapLock.Lock()
	defer c.routeMapLock.Unlock()
	c.routeMap[p] = h
}

func (c *Client) IsAuthed() bool {
	return c.conn().status.Load() == authed
}

func (c *Client) Send(m *proto.Message) error {
	if c.conn().status.Load() != authed {
		return errors.New("connect is not connected")
	}
	msgData, err := m.Marshal()
	if err != nil {
		return err
	}
	data, err := c.proto.Encode(msgData, proto.MsgTypeMessage)
	if err != nil {
		return err
	}

	if c.opts.LogDetailOn {
		c.Info("send message", zap.Uint32("msgType", m.MsgType))
	}

	err = c.conn().asyncWrite(data)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) Request(p string, body []byte) (*proto.Response, error) {

	timeoutCtx, cancel := context.WithTimeout(c.cancelCtx, 10*time.Second)
	defer cancel()
	return c.RequestWithContext(timeoutCtx, p, body)
}

func (c *Client) RequestWithContext(ctx context.Context, p string, body []byte) (*proto.Response, error) {
	if c.conn() == nil {
		return nil, errors.New("conn is nil")
	}
	if c.conn().status.Load() != authed {
		c.Error("connect not authed", zap.String("addr", c.opts.Addr), zap.String("path", p))
		return nil, errors.New("connect not authed")
	}

	r := &proto.Request{
		Id:   uint64(c.reqIDGen.Inc()),
		Path: p,
		Body: body,
	}

	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}

	c.Requesting.Inc()

	msgData, err := c.proto.Encode(data, proto.MsgTypeRequest)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	ch := c.w.Register(r.Id)
	err = c.conn().asyncWrite(msgData)
	if err != nil {
		return nil, err
	}
	cost := time.Since(start)
	if cost > time.Millisecond*100 {
		c.Warn("request cost too long", zap.Duration("cost", cost), zap.String("path", p))
	}
	select {
	case x := <-ch:
		c.Requesting.Dec()
		if x == nil {
			return nil, errors.New("unknown error")
		}
		return x.(*proto.Response), nil
	case <-ctx.Done():
		c.Error("request timeout", zap.String("path", p), zap.Uint64("requestId", r.Id), zap.String("addr", c.opts.Addr), zap.Error(ctx.Err()))
		c.Requesting.Dec()
		c.w.Trigger(r.Id, nil)
		return nil, ctx.Err()
	}
}

func (c *Client) CloseConn() {
	c.conn().close(nil)
}

func (c *Client) conn() *conn {
	return c.conns[0]
}

func (c *Client) handler(p string) Handler {
	c.routeMapLock.RLock()
	defer c.routeMapLock.RUnlock()
	return c.routeMap[p]
}

func (c *Client) handleData(data []byte, msgType proto.MsgType, remoteAddr string) {

	c.conn().idleTick = 0
	c.conn().timeoutTick = 0

	if c.opts.LogDetailOn {
		c.Info("handleData....", zap.Uint8("msgType", msgType.Uint8()), zap.Int("data", len(data)), zap.String("remoteAddr", remoteAddr))
	}

	if msgType == proto.MsgTypeRequest {
		req := &proto.Request{}
		err := req.Unmarshal(data)
		if err != nil {
			c.Debug("unmarshal error", zap.Error(err))
			return
		}
		c.handleRequest(req)
	} else if msgType == proto.MsgTypeResp {
		resp := &proto.Response{}
		err := resp.Unmarshal(data)
		if err != nil {
			c.Debug("unmarshal error", zap.Error(err))
			return
		}
		c.w.Trigger(resp.Id, resp)
	} else if msgType == proto.MsgTypeHeartbeat {

	} else if msgType == proto.MsgTypeConnack {
		connack := &proto.Connack{}
		err := connack.Unmarshal(data)
		if err != nil {
			c.Debug("unmarshal error", zap.Error(err))
			return
		}
		c.w.Trigger(connack.Id, connack)
	} else {
		c.Error("unknown msg type", zap.Uint8("msgType", msgType.Uint8()))
	}
}

func (c *Client) handleRequest(r *proto.Request) {

	handler := c.handler(r.Path)
	if handler == nil {
		c.Info("route not found", zap.String("path", r.Path))
		return
	}
	ctx := NewContext(c)
	ctx.req = r
	handler(ctx)

}
