package server

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

var _ wknet.Conn = (*ProxyClientConn)(nil)

type ProxyClientConn struct {
	id           atomic.Int64
	uid          atomic.String
	deviceLevel  uint8
	deviceFlag   uint8
	deviceID     string
	valueMap     map[string]interface{}
	mu           sync.RWMutex
	s            *Server
	belongNodeID uint64 // 所属节点

	outboundBuffer     *wknet.DefualtBuffer
	inboundBuffer      *wknet.DefualtBuffer
	inboundBufferLock  sync.RWMutex
	outboundBufferLock sync.RWMutex

	closed atomic.Bool

	isAuthed     atomic.Bool   // 是否已认证
	protoVersion atomic.Uint32 // 协议版本

	ctx atomic.Value

	uptime time.Time // 启动时间

	idleTimer *timingwheel.Timer
	maxIdle   atomic.Duration

	lastActivity atomic.Time

	wklog.Log

	connStats *wknet.ConnStats
}

func NewProxyClientConn(s *Server, belongNodeID uint64) *ProxyClientConn {
	p := &ProxyClientConn{
		s:              s,
		valueMap:       make(map[string]interface{}),
		belongNodeID:   belongNodeID,
		outboundBuffer: wknet.NewDefaultBuffer(),
		inboundBuffer:  wknet.NewDefaultBuffer(),
		uptime:         time.Now(),
		Log:            wklog.NewWKLog(fmt.Sprintf("ProxyConn[%d]", belongNodeID)),
		connStats:      wknet.NewConnStats(),
	}
	p.KeepLastActivity()

	return p
}

func (p *ProxyClientConn) ID() int64 {
	return p.id.Load()
}

func (p *ProxyClientConn) SetID(id int64) {
	p.id.Store(id)
}

func (p *ProxyClientConn) UID() string {
	return p.uid.Load()
}
func (p *ProxyClientConn) SetUID(uid string) {
	p.uid.Store(uid)
}

func (p *ProxyClientConn) DeviceLevel() uint8 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.deviceLevel
}

func (p *ProxyClientConn) SetDeviceLevel(level uint8) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deviceLevel = level
}

func (p *ProxyClientConn) DeviceFlag() uint8 {

	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.deviceFlag
}
func (p *ProxyClientConn) SetDeviceFlag(flag uint8) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deviceFlag = flag
}

func (p *ProxyClientConn) DeviceID() string {

	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.deviceID
}

func (p *ProxyClientConn) SetDeviceID(deviceID string) {
	p.deviceID = deviceID
}

func (p *ProxyClientConn) SetValue(key string, value interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.valueMap[key] = value
}

func (p *ProxyClientConn) Value(key string) interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.valueMap[key]
}

func (p *ProxyClientConn) Flush() error {
	return nil
}

func (p *ProxyClientConn) Read(b []byte) (n int, err error) {
	return 0, nil
}

func (p *ProxyClientConn) Peek(n int) ([]byte, error) {
	p.inboundBufferLock.Lock()
	defer p.inboundBufferLock.Unlock()
	if p.inboundBuffer.IsEmpty() {
		return nil, nil
	}
	head, tail := p.inboundBuffer.Peek(n)
	return append(head, tail...), nil
}

func (p *ProxyClientConn) Discard(n int) (int, error) {
	p.inboundBufferLock.Lock()
	defer p.inboundBufferLock.Unlock()
	return p.inboundBuffer.Discard(n)
}

func (p *ProxyClientConn) Write(b []byte) (n int, err error) {

	if p.closed.Load() {
		return -1, net.ErrClosed
	}

	if !p.s.cluster.NodeIsOnline(p.belongNodeID) {
		p.Debug("节点不在线，关闭连接！", zap.Uint64("belongNodeID", p.belongNodeID))
		p.Close()
		return 0, fmt.Errorf("节点不在线！nodeID:%d", p.belongNodeID)
	}
	err = p.s.connectWrite(p.belongNodeID, &rpc.ConnectWriteReq{
		Uid:        p.UID(),
		DeviceFlag: uint32(p.deviceFlag),
		DeviceId:   p.deviceID,
		Data:       b,
	})
	if err != nil {
		p.s.Error("发送数据失败！", zap.Error(err), zap.String("uid", p.UID()), zap.Uint64("belongPeerID", p.belongNodeID))
		return 0, err
	}
	return len(b), nil
}

func (p *ProxyClientConn) WriteToOutboundBuffer(b []byte) (n int, err error) {
	if p.closed.Load() {
		return -1, net.ErrClosed
	}
	p.outboundBufferLock.Lock()
	defer p.outboundBufferLock.Unlock()
	p.KeepLastActivity()
	return p.outboundBuffer.Write(b)
}

// func (p *ProxyClientConn) WriteToInboundBuffer(b []byte) (n int, err error) {
// 	p.KeepLastActivity()

// 	p.inboundBufferLock.Lock()
// 	n, err = p.inboundBuffer.Write(b)
// 	if err != nil {
// 		p.inboundBufferLock.Unlock()
// 		return
// 	}
// 	p.inboundBufferLock.Unlock()
// 	err = p.s.dispatch.dataIn(p)
// 	return
// }

func (p *ProxyClientConn) ReadToInboundBuffer() (int, error) {
	return 0, nil
}

func (p *ProxyClientConn) WakeWrite() error {

	if p.closed.Load() {
		return net.ErrClosed
	}

	if !p.s.cluster.NodeIsOnline(p.belongNodeID) {
		p.Debug("节点不在线，关闭连接！", zap.Uint64("belongNodeID", p.belongNodeID))
		p.Close()
		return fmt.Errorf("节点不在线！nodeID:%d", p.belongNodeID)
	}

	p.outboundBufferLock.Lock()
	head, tail := p.outboundBuffer.Peek(-1)
	p.outboundBufferLock.Unlock()
	if len(head) == 0 && len(tail) == 0 {
		return nil
	}
	msgData := append(head, tail...)
	err := p.s.connectWrite(p.belongNodeID, &rpc.ConnectWriteReq{
		Uid:        p.UID(),
		DeviceFlag: uint32(p.deviceFlag),
		DeviceId:   p.deviceID,
		Data:       msgData,
	})
	if err != nil {
		p.s.Error("发送数据失败！", zap.Error(err), zap.String("uid", p.UID()), zap.Uint64("belongNodeID", p.belongNodeID))
		return err
	}
	p.outboundBufferLock.Lock()
	_, _ = p.outboundBuffer.Discard(len(msgData))
	p.outboundBufferLock.Unlock()
	return nil
}

func (p *ProxyClientConn) Fd() wknet.NetFd {
	return wknet.NetFd{}
}

func (p *ProxyClientConn) IsClosed() bool {
	return p.closed.Load()
}

func (p *ProxyClientConn) Close() error {
	if p.closed.Load() {
		return nil
	}
	p.closed.Store(true)
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}
	p.inboundBufferLock.Lock()
	_ = p.inboundBuffer.Release()
	p.inboundBufferLock.Unlock()
	p.outboundBufferLock.Lock()
	_ = p.outboundBuffer.Release()
	p.outboundBufferLock.Unlock()

	p.s.dispatch.onClose(p)
	return nil
}

func (p *ProxyClientConn) CloseWithErr(er error) error {
	p.Debug("close with error", zap.Error(er), zap.String("conn", p.String()))
	err := p.Close()
	return err
}

func (p *ProxyClientConn) RemoteAddr() net.Addr {
	return nil
}

func (p *ProxyClientConn) LocalAddr() net.Addr {
	return nil
}

func (p *ProxyClientConn) ReactorSub() *wknet.ReactorSub {
	return nil
}

func (p *ProxyClientConn) SetContext(ctx interface{}) {
	p.ctx.Store(ctx)
}

func (p *ProxyClientConn) Context() interface{} {
	return p.ctx.Load()
}

func (p *ProxyClientConn) ConnStats() *wknet.ConnStats {
	return p.connStats
}

func (p *ProxyClientConn) IsAuthed() bool {
	return p.isAuthed.Load()
}

func (p *ProxyClientConn) SetAuthed(authed bool) {
	p.isAuthed.Store(authed)
}

func (p *ProxyClientConn) ProtoVersion() int {
	return int(p.protoVersion.Load())
}

func (p *ProxyClientConn) SetProtoVersion(version int) {
	p.protoVersion.Store(uint32(version))
}

func (p *ProxyClientConn) LastActivity() time.Time {
	return p.lastActivity.Load()
}

func (p *ProxyClientConn) Uptime() time.Time {
	return p.uptime
}

func (p *ProxyClientConn) SetMaxIdle(maxIdle time.Duration) {
	if p.closed.Load() {
		return
	}
	p.maxIdle.Store(maxIdle)
	if p.idleTimer != nil {
		p.idleTimer.Stop()
	}

	if maxIdle > 0 {
		p.idleTimer = p.s.Schedule(maxIdle/2, func() {
			if p.lastActivity.Load().Add(maxIdle).After(time.Now()) {
				return
			}
			p.Debug("max idle time exceeded, close the connection", zap.Duration("maxIdle", maxIdle), zap.Duration("lastActivity", time.Since(p.lastActivity.Load())), zap.String("conn", p.String()))
			if p.idleTimer != nil {
				p.idleTimer.Stop()
			}
			if p.IsClosed() {
				return
			}
			p.Close()
		})
	}
}

func (p *ProxyClientConn) InboundBuffer() wknet.InboundBuffer {
	return p.inboundBuffer
}

func (p *ProxyClientConn) OutboundBuffer() wknet.OutboundBuffer {
	return p.outboundBuffer
}

func (p *ProxyClientConn) SetDeadline(t time.Time) error {
	return nil
}

func (p *ProxyClientConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (p *ProxyClientConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func (p *ProxyClientConn) KeepLastActivity() {
	p.lastActivity.Store(time.Now())
}

func (p *ProxyClientConn) String() string {

	return fmt.Sprintf("ProxyConn[%d] uid=%s deviceFlag=%s deviceLevel=%s deviceID=%s", p.id.Load(), p.uid.Load(), wkproto.DeviceFlag(p.deviceFlag), wkproto.DeviceLevel(p.deviceLevel), p.deviceID)
}
