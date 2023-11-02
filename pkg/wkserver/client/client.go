package client

// import (
// 	"context"
// 	"errors"
// 	"fmt"
// 	"net"
// 	"sync"
// 	"time"

// 	"github.com/WuKongIM/WuKongIM/pkg/wklog"
// 	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
// 	"go.etcd.io/etcd/pkg/v3/idutil"
// 	"go.etcd.io/etcd/pkg/v3/wait"
// 	"go.uber.org/atomic"
// 	"go.uber.org/zap"
// 	gproto "google.golang.org/protobuf/proto"
// )

// type Handler func(c *Context)

// type Client struct {
// 	addr    string
// 	conn    net.Conn
// 	stopped bool
// 	wklog.Log
// 	proto           proto.Protocol
// 	reqIDGen        *idutil.Generator
// 	w               wait.Wait
// 	pingTimer       *time.Timer
// 	opts            *Options
// 	connectStatus   atomic.Uint32
// 	lastActivity    time.Time // 最后一次活动时间
// 	forceDisconnect bool      // 是否强制关闭
// 	listenerConnFnc func(connectStatus ConnectStatus)
// 	doneChan        chan struct{}

// 	routeMapLock sync.RWMutex
// 	routeMap     map[string]Handler

// 	outbound *Outbound
// }

// // New addr: xxx.xxx.xx.xx:xxx
// func New(addr string, opt ...Option) *Client {
// 	opts := NewOptions()
// 	for _, o := range opt {
// 		o(opts)
// 	}
// 	return &Client{
// 		addr:     addr,
// 		Log:      wklog.NewWKLog("Client"),
// 		proto:    proto.New(),
// 		reqIDGen: idutil.NewGenerator(0, time.Now()),
// 		w:        wait.New(),
// 		opts:     opts,
// 		doneChan: make(chan struct{}),
// 		routeMap: make(map[string]Handler),
// 	}
// }

// func (c *Client) Connect() error {

// 	c.forceDisconnect = false
// 	c.connectStatusChange(CONNECTING)
// 	conn, err := net.DialTimeout("tcp", c.addr, c.opts.ConnectTimeout)
// 	if err != nil {
// 		c.connectStatusChange(DISCONNECTED)
// 		c.Debug("connect error", zap.Error(err))
// 		return err
// 	}
// 	c.conn = conn
// 	if c.outbound != nil {
// 		c.outbound.Close()
// 	}
// 	c.outbound = NewOutbound(c.conn, NewOutboundOptions())
// 	c.outbound.Start()
// 	go c.loopRead()

// 	err = c.handshake()
// 	if err != nil {
// 		c.Error("handshake error", zap.Error(err))
// 		return err
// 	}
// 	c.connectStatusChange(CONNECTED)

// 	c.startHeartbeat()
// 	return nil
// }

// func (c *Client) ConnectStatus() ConnectStatus {
// 	return ConnectStatus(c.connectStatus.Load())
// }

// func (c *Client) handshake() error {
// 	conn := &proto.Connect{
// 		Id:    c.reqIDGen.Next(),
// 		Uid:   c.opts.UID,
// 		Token: c.opts.Token,
// 	}
// 	data, err := conn.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	msgData, err := c.proto.Encode(data, proto.MsgTypeConnect.Uint8())
// 	if err != nil {
// 		return err
// 	}

// 	ch := c.w.Register(conn.Id)
// 	err = c.write(msgData)
// 	if err != nil {
// 		return err
// 	}
// 	c.Flush()
// 	timeoutCtx, cancel := context.WithTimeout(context.Background(), c.opts.RequestTimeout)
// 	defer cancel()
// 	select {
// 	case x := <-ch:
// 		if x == nil {
// 			return errors.New("unknown error")
// 		}
// 		ack := x.(*proto.Connack)
// 		if ack.Status != proto.Status_OK {
// 			return errors.New("connect error")
// 		}
// 		return nil
// 	case <-timeoutCtx.Done():
// 		return timeoutCtx.Err()
// 	}
// }

// func (c *Client) Close() error {
// 	c.forceDisconnect = true
// 	c.disconnect()
// 	return nil
// }

// func (c *Client) disconnect() {
// 	c.connectStatusChange(DISCONNECTING)
// 	c.stopped = true
// 	c.stopHeartbeat()
// 	c.conn.Close()
// 	if c.outbound != nil {
// 		c.outbound.Close()
// 		c.outbound = nil
// 	}
// 	<-c.doneChan
// 	c.connectStatusChange(DISCONNECTED)
// }

// // 重连
// func (c *Client) reconnect() error {
// 	c.Warn("reconnecting...")
// 	c.disconnect()
// 	c.connectStatusChange(RECONNECTING)
// 	return c.Connect()
// }

// func (c *Client) loopRead() {
// 	var buff = make([]byte, 10240)
// 	var tmpBuff = make([]byte, 0)
// 	for !c.stopped {
// 		n, err := c.conn.Read(buff)
// 		if err != nil {
// 			c.Debug("read error", zap.Error(err))
// 			goto exit
// 		}
// 		tmpBuff = append(tmpBuff, buff[:n]...)
// 		data, msgType, size, err := c.proto.Decode(tmpBuff)
// 		if err != nil {
// 			c.Debug("decode error", zap.Error(err))
// 			goto exit
// 		}
// 		if size > 0 {
// 			tmpBuff = tmpBuff[size:]
// 		}
// 		if len(data) > 0 {
// 			c.handleData(data, msgType)
// 		}
// 	}
// exit:
// 	c.doneChan <- struct{}{}
// 	if c.needReconnect() {
// 		_ = c.reconnect()
// 	} else {
// 		c.connectStatusChange(CLOSED)
// 	}
// }

// func (c *Client) needReconnect() bool {
// 	if c.opts.Reconnect && !c.forceDisconnect {
// 		return true
// 	}
// 	return false
// }

// func (c *Client) startHeartbeat() {
// 	c.pingTimer = time.AfterFunc(c.opts.HeartbeatInterval, c.processPingTimer)

// }

// func (c *Client) stopHeartbeat() {
// 	c.pingTimer.Stop()
// }

// func (c *Client) processPingTimer() {
// 	if c.connectStatus.Load() != uint32(CONNECTED) {
// 		return
// 	}
// 	if c.lastActivity.Add(c.opts.HeartbeatInterval).Before(time.Now()) {
// 		if c.forceDisconnect {
// 			return
// 		}
// 		err := c.reconnect()
// 		if err != nil {
// 			c.Warn("reconnect error", zap.Error(err))
// 			return
// 		}
// 		return
// 	}
// 	c.sendHeartbeat()
// 	c.pingTimer.Reset(c.opts.HeartbeatInterval)
// }

// func (c *Client) sendHeartbeat() {
// 	fmt.Println()
// 	err := c.write([]byte{proto.MsgTypeHeartbeat.Uint8()})
// 	if err != nil {
// 		c.Warn("send heartbeat error", zap.Error(err))
// 		return
// 	}
// }

// func (c *Client) connectStatusChange(connectStatus ConnectStatus) {
// 	c.connectStatus.Store(uint32(connectStatus))
// 	if c.listenerConnFnc != nil {
// 		c.listenerConnFnc(ConnectStatus(c.connectStatus.Load()))
// 	}
// }

// func (c *Client) handleData(data []byte, msgType proto.MsgType) {

// 	c.lastActivity = time.Now()
// 	if msgType == proto.MsgTypeRequest {
// 		req := &proto.Request{}
// 		err := req.Unmarshal(data)
// 		if err != nil {
// 			c.Debug("unmarshal error", zap.Error(err))
// 			return
// 		}
// 		c.handleRequest(req)
// 	} else if msgType == proto.MsgTypeResp {
// 		resp := &proto.Response{}
// 		err := resp.Unmarshal(data)
// 		if err != nil {
// 			c.Debug("unmarshal error", zap.Error(err))
// 			return
// 		}
// 		c.w.Trigger(resp.Id, resp)
// 	} else if msgType == proto.MsgTypeHeartbeat {
// 		c.Debug("heartbeat...")
// 	} else if msgType == proto.MsgTypeConnack {
// 		connack := &proto.Connack{}
// 		err := connack.Unmarshal(data)
// 		if err != nil {
// 			c.Debug("unmarshal error", zap.Error(err))
// 			return
// 		}
// 		c.w.Trigger(connack.Id, connack)
// 	} else {
// 		c.Error("unknown msg type", zap.Uint8("msgType", msgType.Uint8()))
// 	}

// }

// func (c *Client) handleRequest(r *proto.Request) {

// 	c.routeMapLock.RLock()
// 	h, ok := c.routeMap[r.Path]
// 	c.routeMapLock.RUnlock()
// 	if !ok {
// 		c.Debug("route not found", zap.String("path", r.Path))
// 		return
// 	}
// 	ctx := NewContext(c.conn)
// 	ctx.req = r
// 	h(ctx)

// }

// func (c *Client) RequestWithMessage(p string, protMsg gproto.Message) (*proto.Response, error) {

// 	data, err := gproto.Marshal(protMsg)
// 	if err != nil {
// 		c.Error("marshal error", zap.Error(err))
// 		return nil, err
// 	}
// 	resp, err := c.Request(p, data)
// 	if err != nil {
// 		c.Error("request error", zap.Error(err))
// 		return nil, err
// 	}
// 	return resp, nil

// }

// func (c *Client) Request(p string, body []byte) (*proto.Response, error) {
// 	return c.RequestWithContext(context.Background(), p, body)
// }

// func (c *Client) RequestWithContext(ctx context.Context, p string, body []byte) (*proto.Response, error) {
// 	if c.conn == nil {
// 		return nil, errors.New("conn is nil")
// 	}

// 	r := &proto.Request{
// 		Id:   c.reqIDGen.Next(),
// 		Path: p,
// 		Body: body,
// 	}

// 	data, err := r.Marshal()
// 	if err != nil {
// 		return nil, err
// 	}

// 	msgData, err := c.proto.Encode(data, proto.MsgTypeRequest.Uint8())
// 	if err != nil {
// 		return nil, err
// 	}
// 	ch := c.w.Register(r.Id)
// 	err = c.write(msgData)
// 	if err != nil {
// 		return nil, err
// 	}
// 	timeoutCtx, cancel := context.WithTimeout(context.Background(), c.opts.RequestTimeout)
// 	defer cancel()
// 	select {
// 	case x := <-ch:
// 		if x == nil {
// 			return nil, errors.New("unknown error")
// 		}
// 		return x.(*proto.Response), nil
// 	case <-timeoutCtx.Done():
// 		return nil, timeoutCtx.Err()
// 	case <-ctx.Done():
// 		return nil, ctx.Err()
// 	}
// }

// func (c *Client) Send(m *proto.Message) error {
// 	msgData, err := m.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	data, err := c.proto.Encode(msgData, uint8(proto.MsgTypeMessage))
// 	if err != nil {
// 		return err
// 	}
// 	err = c.write(data)
// 	if err != nil {
// 		return err
// 	}
// 	return nil
// }

// func (c *Client) SendNoFlush(m *proto.Message) error {
// 	msgData, err := m.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	data, err := c.proto.Encode(msgData, uint8(proto.MsgTypeMessage))
// 	if err != nil {
// 		return err
// 	}
// 	if c.outbound != nil {
// 		c.outbound.QueueOutbound(data)
// 	}

// 	return nil
// }

// func (c *Client) write(data []byte) error {
// 	if c.outbound != nil {
// 		err := c.outbound.Write(data)
// 		return err
// 	}
// 	return nil
// }

// func (c *Client) Flush() {
// 	if c.outbound != nil {
// 		c.outbound.Flush()
// 	}
// }

// func (c *Client) Route(p string, h Handler) {
// 	c.routeMapLock.Lock()
// 	defer c.routeMapLock.Unlock()
// 	c.routeMap[p] = h
// }
