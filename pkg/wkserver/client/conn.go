package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/panjf2000/gnet/v2"
	es "github.com/panjf2000/gnet/v2/pkg/errors"

	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type connStatus uint8

const (
	disconnect connStatus = iota // 断开
	connecting                   // 连接中
	connected                    // 已连接
	authing                      // 认证中
	authed                       //已认证

)

type conn struct {
	status      atomic.Value // 是否已认证
	c           *Client
	addr        string // 服务端地址
	gc          gnet.Conn
	idleTick    int // 空闲tick数
	timeoutTick int

	no string // 唯一编号，每次重连都会变

	wklog.Log
}

func newConn(addr string, c *Client) *conn {
	cn := &conn{
		c:    c,
		Log:  wklog.NewWKLog(fmt.Sprintf("conn[%s]", addr)),
		addr: addr,
	}

	cn.status.Store(disconnect)

	return cn
}

func (c *conn) asyncWrite(b []byte) error {
	return c.gc.AsyncWrite(b, nil)
}

func (c *conn) startDial() {

	var proto, addr string
	var err error
	if strings.Contains(c.addr, "://") {
		proto, addr, err = parseProtoAddr(c.addr)
		if err != nil {
			c.Error("parse addr failed", zap.Error(err), zap.String("addr", c.addr))
			c.status.Store(disconnect)
			return
		}
	} else {
		proto = "tcp"
		addr = c.addr
	}
	c.no = wkutil.GenUUID()
	if c.gc != nil {
		_ = c.gc.Close()
		c.gc = nil
	}

	c.gc, err = c.c.cli.DialContext(proto, addr, connCtx{
		no: c.no,
	})
	if err != nil {
		// c.Foucs("conn failed", zap.Error(err))
		c.status.Store(disconnect)
	} else {
		c.status.Store(connected)
		c.Foucs("conn success")
	}
}

func (c *conn) startAuth() {
	err := c.sendAuth()
	if err != nil { // 如果认证失败，断开链接，让其重连
		c.Foucs("auth failed", zap.Error(err))
		c.status.Store(disconnect)
	} else {
		c.status.Store(authed)
		c.Foucs("auth success")
	}
}

func (c *conn) sendAuth() error {
	conn := &proto.Connect{
		Id:    c.c.reqIDGen.Inc(),
		Uid:   c.c.opts.Uid,
		Token: c.c.opts.Token,
	}
	data, err := conn.Marshal()
	if err != nil {
		c.Error("conn auth failed", zap.Error(err))
		return err
	}
	msgData, err := c.c.proto.Encode(data, proto.MsgTypeConnect)
	if err != nil {
		c.Error("conn auth failed", zap.Error(err))
		return err
	}

	waitC := c.c.w.Register(conn.Id)
	err = c.gc.AsyncWrite(msgData, nil)
	if err != nil {
		c.Error("write failed", zap.Error(err))
		return err
	}

	timeoutCtx, cancel := context.WithTimeout(c.c.cancelCtx, time.Second*4)
	defer cancel()
	select {
	case x := <-waitC:
		if x == nil {
			return errors.New("unknown error")
		}
		ack := x.(*proto.Connack)
		if ack.Status != proto.StatusOK {
			return fmt.Errorf("connect error：%d", ack.Status)
		}
		return nil
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	}
}

func (c *conn) tick() {
	c.idleTick++

	// 定时发送心跳
	if c.idleTick >= c.c.opts.HeartbeatTick {
		c.idleTick = 0
		c.sendHeartbeat()
	}

	c.timeoutTick++
	if c.timeoutTick >= c.c.opts.HeartbeatTimeoutTick {
		c.timeoutTick = 0
		c.Foucs("node client heartbeat timeout,reconnect")
		c.reconnect()
	}

}

func (c *conn) reconnect() {
	c.status.Store(disconnect)
	_ = c.gc.Close()
}

// 发送心跳
func (c *conn) sendHeartbeat() {
	data, err := c.c.proto.Encode([]byte{proto.MsgTypeHeartbeat.Uint8()}, proto.MsgTypeHeartbeat)
	if err != nil {
		c.Warn("encode heartbeat error", zap.Error(err))
		return
	}
	err = c.gc.AsyncWrite(data, nil)
	if err != nil {
		c.Warn("send heartbeat error", zap.Error(err))
		return
	}
}

func (c *conn) close(err error) {
	c.status.Store(disconnect)
	c.Foucs("client close", zap.String("id", c.c.opts.Uid), zap.String("addr", c.addr), zap.Error(err))
}

func (c *conn) onTraffic(gc gnet.Conn) gnet.Action {
	if gc.InboundBuffered() == 0 {
		return gnet.None
	}
	batchCount := 0
	for i := 0; i < c.c.batchRead; i++ {
		if gc.InboundBuffered() == 0 {
			break
		}
		data, msgType, _, err := c.c.proto.Decode(gc)
		if err == io.ErrShortBuffer { // 表示数据不够了
			break
		}
		if err != nil {
			c.Foucs("decode error", zap.Error(err))
			return gnet.Close
		}
		if len(data) == 0 {
			break
		}
		batchCount++
		remoteAddr := gc.RemoteAddr().String()
		err = c.c.pool.Submit(func() {
			c.c.handleData(data, msgType, remoteAddr)
		})
		if err != nil {
			c.Error("submit failed", zap.Error(err))
		}
	}

	if batchCount == c.c.batchRead && gc.InboundBuffered() > 0 {
		if gc.InboundBuffered() > 1024*1024 {
			c.Warn("client: inbound buffered is too large", zap.Int("buffered", gc.InboundBuffered()))
		}
		if err := gc.Wake(nil); err != nil { // 这里调用wake避免丢失剩余的数据
			c.Error("failed to wake up the connection", zap.Error(err))
			return gnet.Close
		}
	}
	return gnet.None
}

func parseProtoAddr(protoAddr string) (string, string, error) {
	protoAddr = strings.ToLower(protoAddr)
	if strings.Count(protoAddr, "://") != 1 {
		return "", "", es.ErrInvalidNetworkAddress
	}
	pair := strings.SplitN(protoAddr, "://", 2)
	proto, addr := pair[0], pair[1]
	switch proto {
	case "tcp", "tcp4", "tcp6", "udp", "udp4", "udp6", "unix":
	default:
		return "", "", es.ErrUnsupportedProtocol
	}
	if addr == "" {
		return "", "", es.ErrInvalidNetworkAddress
	}
	return proto, addr, nil
}

type connCtx struct {
	no string
}
