package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.etcd.io/etcd/pkg/v3/wait"
	"go.uber.org/zap"
	gproto "google.golang.org/protobuf/proto"
)

type Handler func(c *Context)

type Client struct {
	addr string
	conn net.Conn
	opts *Options
	wklog.Log
	proto    proto.Protocol
	outbound *Outbound
	reqIDGen *idutil.Generator
	w        wait.Wait

	routeMapLock sync.RWMutex
	routeMap     map[string]Handler
	lastActivity atomic.Time // 最后一次活动时间

	pingLock  sync.Mutex
	pingTimer *time.Timer

	connectStatus   atomic.Uint32
	forceDisconnect bool // 是否强制关闭

	cacheBuff []byte
	tmpBuff   []byte
	running   atomic.Bool
	stopped   atomic.Bool
}

func New(addr string, opt ...Option) *Client {
	opts := NewOptions()
	for _, o := range opt {
		o(opts)
	}
	return &Client{
		addr:      addr,
		Log:       wklog.NewWKLog(fmt.Sprintf("Client[%s]", opts.UID)),
		proto:     proto.New(),
		opts:      opts,
		reqIDGen:  idutil.NewGenerator(0, time.Now()),
		w:         wait.New(),
		routeMap:  make(map[string]Handler),
		cacheBuff: make([]byte, 10240),
		tmpBuff:   make([]byte, 0),
	}
}

func (c *Client) Start() {
	go c.run(nil)
}

func (c *Client) Connect() error {
	connectChan := make(chan struct{})
	go c.run(connectChan)
	<-connectChan
	return nil
}

func (c *Client) Close() error {
	c.forceDisconnect = true
	c.Debug("client close")
	if c.conn != nil {
		c.conn.Close()
	}
	return nil
}

// LastActivity 最后一次活动时间
func (c *Client) LastActivity() time.Time {
	return c.lastActivity.Load()
}

func (c *Client) SetActivity(t time.Time) {
	c.lastActivity.Store(t)
}

func (c *Client) keepActivity() {
	c.lastActivity.Store(time.Now())
}

func (c *Client) run(connectChan chan struct{}) {
	errSleepDuri := time.Second * 1
	if c.running.Load() {
		return
	}
	for {
		if c.forceDisconnect {
			return
		}

		c.running.Store(true)
		c.disconnect()

		c.connectStatusChange(CONNECTING)
		// 建立连接
		conn, err := net.DialTimeout("tcp", c.addr, c.opts.ConnectTimeout)
		if err != nil {
			// 处理错误
			c.Info("connect is error", zap.Error(err))
			time.Sleep(errSleepDuri)
			continue
		}
		c.conn = conn
		c.lastActivity.Store(time.Now())
		opts := NewOutboundOptions()
		opts.OnClose = c.onOutboundClose
		c.outbound = NewOutbound(c.conn, opts)
		c.outbound.Start()
		err = c.handshake()
		if err != nil {
			c.Warn("handshake is error", zap.Error(err))
			time.Sleep(errSleepDuri)
			continue
		}
		c.connectStatusChange(CONNECTED)
		if connectChan != nil {
			connectChan <- struct{}{}
			connectChan = nil
		}

		c.startHeartbeat()

		c.loopRead()
	}
}

func (c *Client) onOutboundClose() {
	c.Debug("outbound close")
	c.stopped.Store(true)
}

func (c *Client) startHeartbeat() {
	c.pingLock.Lock()
	defer c.pingLock.Unlock()
	c.pingTimer = time.AfterFunc(c.opts.HeartbeatInterval, c.processPingTimer)
}

func (c *Client) stopHeartbeat() {
	if c.pingTimer != nil {
		c.pingTimer.Stop()
	}
}

func (c *Client) processPingTimer() {
	if c.connectStatus.Load() != uint32(CONNECTED) {
		return
	}
	if c.lastActivity.Load().Add(c.opts.HeartbeatInterval * 3).Before(time.Now()) {
		if c.forceDisconnect {
			return
		}
		c.Error("heartbeat timeout")
		c.disconnect()
		return
	}
	c.sendHeartbeat()
	c.pingLock.Lock()
	c.pingTimer.Reset(c.opts.HeartbeatInterval)
	c.pingLock.Unlock()
}

func (c *Client) handshake() error {
	conn := &proto.Connect{
		Id:    c.reqIDGen.Next(),
		Uid:   c.opts.UID,
		Token: c.opts.Token,
	}
	data, err := conn.Marshal()
	if err != nil {
		return err
	}
	msgData, err := c.proto.Encode(data, proto.MsgTypeConnect.Uint8())
	if err != nil {
		return err
	}

	ch := c.w.Register(conn.Id)
	err = c.write(msgData)
	if err != nil {
		return err
	}
	c.Flush()
	err = c.read()
	if err != nil {
		return err
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), c.opts.RequestTimeout)
	defer cancel()
	select {
	case x := <-ch:
		if x == nil {
			return errors.New("unknown error")
		}
		ack := x.(*proto.Connack)
		if ack.Status != proto.Status_OK {
			return errors.New("connect error")
		}
		return nil
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	}
}

func (c *Client) write(data []byte) error {
	if c.outbound != nil {
		err := c.outbound.Write(data)
		return err
	}
	return nil
}

func (c *Client) sendHeartbeat() {
	err := c.write([]byte{proto.MsgTypeHeartbeat.Uint8()})
	if err != nil {
		c.Warn("send heartbeat error", zap.Error(err))
		return
	}
	c.Flush()
}

func (c *Client) disconnect() {
	if c.connectStatus.Load() == uint32(UNKNOWN) {
		return
	}
	c.connectStatusChange(DISCONNECTING)
	c.stopHeartbeat()
	if c.conn != nil {
		c.conn.Close()
	}
	if c.outbound != nil {
		c.outbound.Close()
		c.outbound = nil
	}
	c.connectStatusChange(DISCONNECTED)
}

func (c *Client) loopRead() {
	for !c.stopped.Load() {
		err := c.read()
		if err != nil {
			c.Debug("read error", zap.Error(err))
			return
		}
	}
}

func (c *Client) read() error {
	n, err := c.conn.Read(c.cacheBuff)
	if err != nil {
		return err
	}
	c.tmpBuff = append(c.tmpBuff, c.cacheBuff[:n]...)
	for {
		data, msgType, size, err := c.proto.Decode(c.tmpBuff)
		if err != nil {
			return err
		}
		if size == 0 {
			break
		}

		if size > 0 {
			c.tmpBuff = c.tmpBuff[size:]
		}
		if len(data) > 0 {
			c.handleData(data, msgType)
		}
	}
	return nil
}

func (c *Client) Flush() {
	if c.outbound != nil {
		c.outbound.Flush()
	}
}

func (c *Client) handleData(data []byte, msgType proto.MsgType) {
	c.lastActivity.Store(time.Now())
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
		// c.Debug("收到请求返回", zap.Uint64("reqID", resp.Id), zap.Int64("cost", time.Now().UnixMilli()-resp.Timestamp))
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

	c.routeMapLock.RLock()
	h, ok := c.routeMap[r.Path]
	c.routeMapLock.RUnlock()
	if !ok {
		c.Debug("route not found", zap.String("path", r.Path))
		return
	}
	ctx := NewContext(c.conn)
	ctx.req = r
	h(ctx)

}

func (c *Client) RequestWithMessage(p string, protMsg gproto.Message) (*proto.Response, error) {

	data, err := gproto.Marshal(protMsg)
	if err != nil {
		c.Error("marshal error", zap.Error(err))
		return nil, err
	}
	resp, err := c.Request(p, data)
	if err != nil {
		c.Error("request error", zap.Error(err))
		return nil, err
	}
	return resp, nil

}

func (c *Client) Request(p string, body []byte) (*proto.Response, error) {
	return c.RequestWithContext(context.Background(), p, body)
}

func (c *Client) RequestWithContext(ctx context.Context, p string, body []byte) (*proto.Response, error) {
	if c.conn == nil {
		return nil, errors.New("conn is nil")
	}

	r := &proto.Request{
		Id:   c.reqIDGen.Next(),
		Path: p,
		Body: body,
	}

	data, err := r.Marshal()
	if err != nil {
		return nil, err
	}

	msgData, err := c.proto.Encode(data, proto.MsgTypeRequest.Uint8())
	if err != nil {
		return nil, err
	}
	ch := c.w.Register(r.Id)
	err = c.write(msgData)
	if err != nil {
		return nil, err
	}
	c.Flush()
	timeoutCtx, cancel := context.WithTimeout(ctx, c.opts.RequestTimeout)
	defer cancel()
	select {
	case x := <-ch:
		if x == nil {
			return nil, errors.New("unknown error")
		}
		return x.(*proto.Response), nil
	case <-timeoutCtx.Done():
		timeoutCtx.Deadline()
		return nil, timeoutCtx.Err()
	}
}

func (c *Client) connectStatusChange(connectStatus ConnectStatus) {
	c.connectStatus.Store(uint32(connectStatus))
	if c.opts.OnConnectStatus != nil {
		c.opts.OnConnectStatus(connectStatus)
	}
}

func (c *Client) ConnectStatus() ConnectStatus {
	return ConnectStatus(c.connectStatus.Load())
}

func (c *Client) Route(p string, h Handler) {
	c.routeMapLock.Lock()
	defer c.routeMapLock.Unlock()
	c.routeMap[p] = h
}

func (c *Client) Send(m *proto.Message) error {
	if c.connectStatus.Load() != uint32(CONNECTED) {
		return errors.New("connect is not connected")
	}
	msgData, err := m.Marshal()
	if err != nil {
		return err
	}
	data, err := c.proto.Encode(msgData, uint8(proto.MsgTypeMessage))
	if err != nil {
		return err
	}
	err = c.write(data)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) SendBatch(msgs []*proto.Message) error {
	if c.connectStatus.Load() != uint32(CONNECTED) {
		return errors.New("connect is not connected")
	}
	for _, m := range msgs {
		msgData, err := m.Marshal()
		if err != nil {
			return err
		}
		data, err := c.proto.Encode(msgData, uint8(proto.MsgTypeMessage))
		if err != nil {
			return err
		}
		if c.outbound != nil {
			c.outbound.QueueOutbound(data)
		}
	}
	c.Flush()
	return nil
}

func (c *Client) SendNoFlush(m *proto.Message) error {
	if c.connectStatus.Load() != uint32(CONNECTED) {
		return errors.New("connect is not connected")
	}
	msgData, err := m.Marshal()
	if err != nil {
		return err
	}

	data, err := c.proto.Encode(msgData, uint8(proto.MsgTypeMessage))
	if err != nil {
		return err
	}
	if c.outbound != nil {
		c.outbound.QueueOutbound(data)
	}

	return nil
}
