package client

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.etcd.io/etcd/pkg/v3/wait"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Client struct {
	addr    string
	conn    net.Conn
	stopped bool
	wklog.Log
	proto           proto.Protocol
	reqIDGen        *idutil.Generator
	w               wait.Wait
	pingTimer       *time.Timer
	opts            *Options
	connectStatus   atomic.Uint32
	lastActivity    time.Time // 最后一次活动时间
	forceDisconnect bool      // 是否强制关闭
	listenerConnFnc func(connectStatus ConnectStatus)
	doneChan        chan struct{}
}

func New(addr string) *Client {
	opts := NewOptions()
	return &Client{
		addr:     addr,
		Log:      wklog.NewWKLog("Client"),
		proto:    proto.New(),
		reqIDGen: idutil.NewGenerator(0, time.Now()),
		w:        wait.New(),
		opts:     opts,
		doneChan: make(chan struct{}),
	}
}

func (c *Client) Connect() error {
	c.forceDisconnect = false
	c.connectStatusChange(CONNECTING)
	conn, err := net.DialTimeout("tcp", c.addr, c.opts.ConnectTimeout)
	if err != nil {
		c.connectStatusChange(DISCONNECTED)
		return err
	}
	c.conn = conn
	c.connectStatusChange(CONNECTED)
	go c.loopRead()
	c.startHeartbeat()
	return nil
}

func (c *Client) Close() error {
	c.stopped = true
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *Client) disconnect() {
	c.connectStatusChange(DISCONNECTING)
	c.stopped = true
	c.stopHeartbeat()
	c.conn.Close()

	<-c.doneChan
	c.connectStatusChange(DISCONNECTED)
}

// 重连
func (c *Client) reconnect() error {
	c.disconnect()
	c.connectStatusChange(RECONNECTING)
	return c.Connect()
}

func (c *Client) loopRead() {
	var buff = make([]byte, 10240)
	var tmpBuff = make([]byte, 0)
	for !c.stopped {
		n, err := c.conn.Read(buff)
		if err != nil {
			c.Debug("read error", zap.Error(err))
			goto exit
		}
		tmpBuff = append(tmpBuff, buff[:n]...)
		data, msgType, size, err := c.proto.Decode(tmpBuff)
		if err != nil {
			c.Debug("decode error", zap.Error(err))
			goto exit
		}
		if size > 0 {
			tmpBuff = tmpBuff[size:]
		}
		if len(data) > 0 {
			c.handleData(data, msgType)
		}
	}
exit:
	c.doneChan <- struct{}{}
	if c.needReconnect() {
		_ = c.reconnect()
	} else {
		c.connectStatusChange(CLOSED)
	}
}

func (c *Client) needReconnect() bool {
	if c.opts.Reconnect && !c.forceDisconnect {
		return true
	}
	return false
}

func (c *Client) startHeartbeat() {
	c.pingTimer = time.AfterFunc(c.opts.HeartbeatInterval, c.processPingTimer)

}

func (c *Client) stopHeartbeat() {
	c.pingTimer.Stop()
}

func (c *Client) processPingTimer() {
	if c.connectStatus.Load() != uint32(CONNECTED) {
		return
	}
	if c.lastActivity.Add(c.opts.HeartbeatInterval).Before(time.Now()) {
		if c.forceDisconnect {
			return
		}
		err := c.reconnect()
		if err != nil {
			c.Warn("reconnect error", zap.Error(err))
			return
		}
		return
	}
	c.sendHeartbeat()
	c.pingTimer.Reset(c.opts.HeartbeatInterval)
}

func (c *Client) sendHeartbeat() {
	_, err := c.conn.Write([]byte{proto.MsgTypeHeartbeat.Uint8()})
	if err != nil {
		c.Warn("send heartbeat error", zap.Error(err))
		return
	}
}

func (c *Client) connectStatusChange(connectStatus ConnectStatus) {
	c.connectStatus.Store(uint32(connectStatus))
	if c.listenerConnFnc != nil {
		c.listenerConnFnc(ConnectStatus(c.connectStatus.Load()))
	}
}

func (c *Client) handleData(data []byte, msgType proto.MsgType) {

	c.lastActivity = time.Now()
	if msgType == proto.MsgTypeResp {
		resp := &proto.Response{}
		err := resp.Unmarshal(data)
		if err != nil {
			c.Debug("unmarshal error", zap.Error(err))
			return
		}
		c.w.Trigger(resp.Id, resp)
	} else if msgType == proto.MsgTypeHeartbeat {
		c.Debug("heartbeat...")
	} else {
		c.Error("unknown msg type", zap.Uint8("msgType", msgType.Uint8()))
	}

}

func (c *Client) Request(p string, body []byte) (*proto.Response, error) {
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
	_, err = c.conn.Write(msgData)
	if err != nil {
		return nil, err
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), c.opts.RequestTimeout)
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
