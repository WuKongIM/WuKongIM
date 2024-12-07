package wknet

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/ring"
	"github.com/WuKongIM/crypto/tls"
	"github.com/sasha-s/go-deadlock"

	"go.uber.org/atomic"
	"go.uber.org/zap"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type ConnStats struct {
	InMsgs         atomic.Int64 // 收到客户端消息数量
	OutMsgs        atomic.Int64 // 下发消息数量
	InMsgBytes     atomic.Int64 // 收到消息字节数
	OutMsgBytes    atomic.Int64 // 下发消息字节数
	InPackets      atomic.Int64 // 收到包数量
	OutPackets     atomic.Int64 // 下发包数量
	InPacketBytes  atomic.Int64 // 收到包字节数
	OutPacketBytes atomic.Int64 // 下发包字节数
}

func NewConnStats() *ConnStats {

	return &ConnStats{}
}

type Conn interface {
	// ID returns the connection id.
	ID() int64
	// SetID sets the connection id.
	SetID(id int64)
	// UID returns the user uid.
	UID() string
	// SetUID sets the user uid.
	SetUID(uid string)
	// DeviceLevel() uint8
	// SetDeviceLevel(deviceLevel uint8)
	// DeviceFlag returns the device flag.
	// DeviceFlag() uint8
	// SetDeviceFlag sets the device flag.
	// SetDeviceFlag(deviceFlag uint8)
	// DeviceID returns the device id.
	// DeviceID() string
	// SetValue sets the value associated with key to value.
	SetValue(key string, value interface{})
	// Value returns the value associated with key.
	Value(key string) interface{}
	// SetDeviceID sets the device id.
	// SetDeviceID(deviceID string)
	// Flush flushes the data to the connection.
	Flush() error
	// Read reads the data from the connection.
	Read(buf []byte) (int, error)
	// Peek peeks the data from the connection.
	Peek(n int) ([]byte, error)
	// Discard discards the data from the connection.
	Discard(n int) (int, error)
	// Write writes the data to the connection. TODO: Locking is required when calling write externally
	Write(b []byte) (int, error)
	// WriteToOutboundBuffer writes the data to the outbound buffer.  Thread safety
	WriteToOutboundBuffer(b []byte) (int, error)
	// Wake wakes up the connection write.
	WakeWrite() error
	// Fd returns the file descriptor of the connection.
	Fd() NetFd
	// IsClosed returns true if the connection is closed.
	IsClosed() bool
	// Close closes the connection.
	Close() error
	CloseWithErr(err error) error
	// RemoteAddr returns the remote network address.
	RemoteAddr() net.Addr
	// SetRemoteAddr sets the remote network address.
	SetRemoteAddr(addr net.Addr)
	// LocalAddr returns the local network address.
	LocalAddr() net.Addr
	// ReactorSub returns the reactor sub.
	ReactorSub() *ReactorSub
	// ReadToInboundBuffer read data from connection and  write to inbound buffer
	ReadToInboundBuffer() (int, error)
	SetContext(ctx interface{})
	Context() interface{}
	// IsAuthed returns true if the connection is authed.
	IsAuthed() bool
	// SetAuthed sets the connection is authed.
	SetAuthed(authed bool)
	// LastActivity returns the last activity time.
	LastActivity() time.Time
	// Uptime returns the connection uptime.
	Uptime() time.Time
	// SetMaxIdle sets the connection max idle time.
	// If the connection is idle for more than the specified duration, it will be closed.
	SetMaxIdle(duration time.Duration)

	InboundBuffer() InboundBuffer
	OutboundBuffer() OutboundBuffer

	SetDeadline(t time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error

	// ConnStats returns the connection stats.
	// ConnStats() *ConnStats
}

type IWSConn interface {
	WriteServerBinary(data []byte) error
}

type DefaultConn struct {
	fd             NetFd
	remoteAddr     net.Addr
	localAddr      net.Addr
	eg             *Engine
	reactorSub     *ReactorSub
	inboundBuffer  InboundBuffer  // inboundBuffer InboundBuffer
	outboundBuffer OutboundBuffer // outboundBuffer OutboundBuffer
	closed         atomic.Bool    // if the connection is closed
	isWAdded       bool           // if the connection is added to the write event
	mu             deadlock.RWMutex
	context        atomic.Value
	authed         atomic.Bool // if the connection is authed
	id             atomic.Int64
	uid            atomic.String
	valueMap       sync.Map

	uptime       atomic.Time
	lastActivity atomic.Time
	maxIdle      time.Duration
	maxIdleLock  sync.RWMutex

	idleTimer *timingwheel.Timer

	wklog.Log
}

func GetDefaultConn(id int64, connFd NetFd, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) *DefaultConn {
	defaultConn := eg.defaultConnPool.Get().(*DefaultConn)
	defaultConn.id.Store(id)
	defaultConn.fd = connFd
	defaultConn.remoteAddr = remoteAddr
	defaultConn.localAddr = localAddr
	defaultConn.isWAdded = false
	defaultConn.authed.Store(false)
	defaultConn.closed.Store(false)
	defaultConn.uid.Store("")
	defaultConn.eg = eg
	defaultConn.reactorSub = reactorSub
	defaultConn.valueMap = sync.Map{}
	defaultConn.context = atomic.Value{}
	defaultConn.lastActivity.Store(time.Now())
	defaultConn.maxIdle = 0
	defaultConn.uptime.Store(time.Now())
	defaultConn.Log = wklog.NewWKLog(fmt.Sprintf("Conn[[reactor-%d]%d]", reactorSub.idx, id))

	defaultConn.inboundBuffer = eg.eventHandler.OnNewInboundConn(defaultConn, eg)
	defaultConn.outboundBuffer = eg.eventHandler.OnNewOutboundConn(defaultConn, eg)

	return defaultConn
}

func CreateConn(id int64, connFd NetFd, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {

	// defaultConn := &DefaultConn{
	// 	id:         id,
	// 	fd:         connFd,
	// 	remoteAddr: remoteAddr,
	// 	localAddr:  localAddr,
	// 	eg:         eg,
	// 	reactorSub: reactorSub,
	// 	closed:     false,
	// 	valueMap:   map[string]interface{}{},
	// 	uptime:     time.Now(),
	// 	Log:        wklog.NewWKLog(fmt.Sprintf("Conn[%d]", id)),
	// }

	defaultConn := GetDefaultConn(id, connFd, localAddr, remoteAddr, eg, reactorSub)
	if eg.options.TCPTLSConfig != nil {
		tc := newTLSConn(defaultConn)
		tlsCn := tls.Server(tc, eg.options.TCPTLSConfig)
		tc.tlsconn = tlsCn
		return tc, nil
	}
	return defaultConn, nil
}

func (d *DefaultConn) ID() int64 {
	return d.id.Load()
}

func (d *DefaultConn) SetID(id int64) {
	d.id.Store(id)
}

func (d *DefaultConn) ReadToInboundBuffer() (int, error) {
	readBuffer := d.reactorSub.ReadBuffer
	n, err := d.fd.Read(readBuffer)
	if err != nil || n == 0 {
		return 0, err
	}
	if d.eg.options.Event.OnReadBytes != nil {
		d.eg.options.Event.OnReadBytes(n)
	}
	if d.overflowForInbound(n) {
		return 0, fmt.Errorf("inbound buffer overflow, fd: %d buffSize:%d n: %d currentSize: %d maxSize: %d", d.fd, d.inboundBuffer.BoundBufferSize(), n, d.inboundBuffer.BoundBufferSize()+n, d.eg.options.MaxReadBufferSize)
	}
	d.KeepLastActivity()
	_, err = d.inboundBuffer.Write(readBuffer[:n])
	return n, err
}

func (d *DefaultConn) KeepLastActivity() {
	d.lastActivity.Store(time.Now())
}

func (d *DefaultConn) Read(buf []byte) (int, error) {
	if d.inboundBuffer.IsEmpty() {
		return 0, nil
	}
	n, err := d.inboundBuffer.Read(buf)
	if n == len(buf) {
		return n, nil
	}
	return n, err
}

func (d *DefaultConn) Write(b []byte) (int, error) {
	if d.closed.Load() {
		return -1, net.ErrClosed
	}
	// 这里不能使用d.mu上锁，否则会导致死锁 WSSConn死锁
	// d.mu.Lock()
	// defer d.mu.Unlock()
	n, err := d.write(b)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// write to outbound buffer
func (d *DefaultConn) WriteToOutboundBuffer(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if d.closed.Load() {
		return -1, net.ErrClosed
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outboundBuffer.Write(b)

}

func (d *DefaultConn) WakeWrite() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed.Load() {
		return net.ErrClosed
	}
	return d.addWriteIfNotExist()
}

func (d *DefaultConn) IsClosed() bool {

	return d.closed.Load()
}

func (d *DefaultConn) Flush() error {
	if d.closed.Load() {
		return net.ErrClosed
	}
	return d.flush()
}
func (d *DefaultConn) Fd() NetFd {

	return d.fd
}

// 调用次方法需要加锁
func (d *DefaultConn) close(closeErr error) error {

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed.Load() {
		return nil
	}
	d.closed.Store(true)

	if closeErr != nil && !errors.Is(closeErr, syscall.ECONNRESET) { // closeErr有值，说明来自系统底层的错误，如果closeErr=nil目前了解的是不需要DeleteFd，ECONNRESET表示fd已经关闭，不需要再次关闭
		err := d.reactorSub.DeleteFd(d) // 先删除fd
		if err != nil {
			d.Debug("delete fd from poller error", zap.Error(err), zap.Int("fd", d.Fd().fd), zap.String("uid", d.uid.Load()))
		}
	}

	_ = d.fd.Close()             // 后关闭fd
	d.eg.RemoveConn(d)           // remove from the engine
	d.reactorSub.ConnDec()       // decrease the connection count
	d.eg.eventHandler.OnClose(d) // call the close handler

	d.release()

	return nil
}

func (d *DefaultConn) Close() error {
	return d.close(nil)
}

func (d *DefaultConn) CloseWithErr(err error) error {

	return d.close(err)
}

func (d *DefaultConn) RemoteAddr() net.Addr {

	return d.remoteAddr
}

func (d *DefaultConn) SetRemoteAddr(addr net.Addr) {
	d.remoteAddr = addr
}

func (d *DefaultConn) LocalAddr() net.Addr {
	return d.localAddr
}

func (d *DefaultConn) SetDeadline(t time.Time) error {
	if err := d.SetReadDeadline(t); err != nil {
		return err
	}
	return d.SetWriteDeadline(t)
}

func (d *DefaultConn) SetReadDeadline(t time.Time) error {
	return ErrUnsupportedOp
}

func (d *DefaultConn) SetWriteDeadline(t time.Time) error {
	return ErrUnsupportedOp
}

func (d *DefaultConn) release() {

	d.Debug("release connection")
	d.fd = NetFd{}
	d.maxIdle = 0
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
	err := d.inboundBuffer.Release()
	if err != nil {
		d.Debug("inboundBuffer release error", zap.Error(err))
	}
	err = d.outboundBuffer.Release()
	if err != nil {
		d.Debug("outboundBuffer release error", zap.Error(err))
	}

	d.eg.defaultConnPool.Put(d)

}

func (d *DefaultConn) Peek(n int) ([]byte, error) {
	totalLen := d.inboundBuffer.BoundBufferSize()
	if n > totalLen {
		return nil, io.ErrShortBuffer
	} else if n <= 0 {
		n = totalLen
	}
	if d.inboundBuffer.IsEmpty() {
		return nil, nil
	}
	head, tail := d.inboundBuffer.Peek(n)
	d.reactorSub.cache.Reset()
	d.reactorSub.cache.Write(head)
	d.reactorSub.cache.Write(tail)

	data := d.reactorSub.cache.Bytes()

	resultData := make([]byte, len(data))
	copy(resultData, data) // TODO: 这里需要复制一份，否则多线程下解析数据包会有问题 本人测试 15个连接15个消息 在协程下打印sendPacket的payload会有数据错误问题

	return resultData, nil
}

func (d *DefaultConn) Discard(n int) (int, error) {
	return d.inboundBuffer.Discard(n)
}

func (d *DefaultConn) ReactorSub() *ReactorSub {
	return d.reactorSub
}

func (d *DefaultConn) SetContext(ctx interface{}) {
	d.context.Store(ctx)
}
func (d *DefaultConn) Context() interface{} {
	return d.context.Load()
}

func (d *DefaultConn) IsAuthed() bool {
	return d.authed.Load()
}
func (d *DefaultConn) SetAuthed(authed bool) {
	d.authed.Store(authed)
}

func (d *DefaultConn) UID() string {
	return d.uid.Load()
}
func (d *DefaultConn) SetUID(uid string) {
	d.uid.Store(uid)
}

func (d *DefaultConn) SetValue(key string, value interface{}) {
	d.valueMap.Store(key, value)
}
func (d *DefaultConn) Value(key string) interface{} {

	value, _ := d.valueMap.Load(key)
	return value
}

func (d *DefaultConn) InboundBuffer() InboundBuffer {
	return d.inboundBuffer
}

func (d *DefaultConn) OutboundBuffer() OutboundBuffer {
	return d.outboundBuffer
}

func (d *DefaultConn) LastActivity() time.Time {
	return d.lastActivity.Load()
}

func (d *DefaultConn) Uptime() time.Time {
	return d.uptime.Load()
}

func (d *DefaultConn) SetMaxIdle(maxIdle time.Duration) {

	d.maxIdleLock.Lock()
	defer d.maxIdleLock.Unlock()

	if d.closed.Load() {
		d.Debug("connection is closed, setMaxIdle failed")
		return
	}

	d.maxIdle = maxIdle

	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}

	if maxIdle > 0 {
		d.idleTimer = d.eg.Schedule(maxIdle/2, func() {
			d.maxIdleLock.Lock()
			defer d.maxIdleLock.Unlock()

			if d.lastActivity.Load().Add(maxIdle).After(time.Now()) {
				return
			}
			d.Debug("max idle time exceeded, close the connection", zap.Duration("maxIdle", maxIdle), zap.Duration("lastActivity", time.Since(d.lastActivity.Load())), zap.String("conn", d.String()))
			if d.idleTimer != nil {
				d.idleTimer.Stop()
				d.idleTimer = nil
			}
			if d.closed.Load() {
				return
			}
			_ = d.close(nil)
		})
	}
}

func (d *DefaultConn) flush() error {

	d.mu.Lock()
	// defer  d.mu.Unlock() // 这里不能defer锁，因为d.reactorSub.CloseConn里也会调用close的锁，导致死锁

	if d.closed.Load() {
		d.mu.Unlock()
		return net.ErrClosed
	}

	if d.outboundBuffer.IsEmpty() {
		d.mu.Unlock()
		_ = d.removeWriteIfExist()
		return nil
	}
	var (
		n   int
		err error
	)

	head, tail := d.outboundBuffer.Peek(-1)
	n, err = d.writeDirect(head, tail)
	_, _ = d.outboundBuffer.Discard(n)
	if d.eg.options.Event.OnWirteBytes != nil {
		d.eg.options.Event.OnWirteBytes(n)
	}
	d.mu.Unlock()

	switch err {
	case nil:
	case syscall.EAGAIN:
		d.Error("write error", zap.Error(err))
	default:
		// d.reactorSub.CloseConn 里使用了d.mu的锁
		err = d.reactorSub.CloseConn(d, os.NewSyscallError("write", err))
		if err != nil {
			d.Error("failed to close conn", zap.Error(err))
			return err
		}
	}
	// All data have been drained, it's no need to monitor the writable events,
	// remove the writable event from poller to help the future event-loops.

	d.mu.Lock()
	if d.outboundBuffer.IsEmpty() {
		_ = d.removeWriteIfExist()
	}
	d.mu.Unlock()
	return nil

}

func (d *DefaultConn) WriteDirect(head, tail []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writeDirect(head, tail)
}

func (d *DefaultConn) writeDirect(head, tail []byte) (int, error) {
	if d.closed.Load() {
		return -1, net.ErrClosed
	}
	var (
		n   int
		err error
	)
	if len(head) > 0 && len(tail) > 0 {
		n, err = d.fd.Write(append(head, tail...))
	} else {
		if len(head) > 0 {
			n, err = d.fd.Write(head)
		} else if len(tail) > 0 {
			n, err = d.fd.Write(tail)
		}
	}
	return n, err
}

func (d *DefaultConn) write(b []byte) (int, error) {
	if d.closed.Load() {
		return -1, net.ErrClosed
	}
	n := len(b)
	if n == 0 {
		return 0, nil
	}
	if d.overflowForOutbound(len(b)) { // overflow check
		return 0, syscall.EINVAL
	}
	var err error
	n, err = d.outboundBuffer.Write(b)
	if err != nil {
		return 0, err
	}
	if err = d.addWriteIfNotExist(); err != nil {
		return n, err
	}
	return n, nil
}

func (d *DefaultConn) addWriteIfNotExist() error {
	if d.closed.Load() {
		return net.ErrClosed
	}
	return d.reactorSub.AddWrite(d)
}

func (d *DefaultConn) removeWriteIfExist() error {
	// if d.isWAdded {
	// 	d.isWAdded = false
	// 	return d.reactorSub.RemoveWrite(d)
	// }
	if d.closed.Load() {
		return net.ErrClosed
	}
	return d.reactorSub.RemoveWrite(d)
}

func (d *DefaultConn) overflowForOutbound(n int) bool {
	maxWriteBufferSize := d.eg.options.MaxWriteBufferSize
	return maxWriteBufferSize > 0 && (d.outboundBuffer.BoundBufferSize()+n > maxWriteBufferSize)
}
func (d *DefaultConn) overflowForInbound(n int) bool {
	maxReadBufferSize := d.eg.options.MaxReadBufferSize
	return maxReadBufferSize > 0 && (d.inboundBuffer.BoundBufferSize()+n > maxReadBufferSize)
}

func (d *DefaultConn) String() string {

	return fmt.Sprintf("Conn[%d] fd=%d", d.id.Load(), d.fd)
}

type TLSConn struct {
	d                *DefaultConn
	tlsconn          *tls.Conn
	tmpInboundBuffer InboundBuffer // inboundBuffer InboundBuffer
}

func newTLSConn(d *DefaultConn) *TLSConn {

	return &TLSConn{
		d:                d,
		tmpInboundBuffer: d.eg.eventHandler.OnNewInboundConn(d, d.eg),
	}
}

func (t *TLSConn) ReadToInboundBuffer() (int, error) {
	readBuffer := t.d.reactorSub.ReadBuffer
	n, err := t.d.fd.Read(readBuffer)
	if err != nil || n == 0 {
		return 0, err
	}
	if t.d.eg.options.Event.OnReadBytes != nil {
		t.d.eg.options.Event.OnReadBytes(n)
	}
	_, err = t.tmpInboundBuffer.Write(readBuffer[:n]) // 将tls加密的内容写到tmpInboundBuffer内， tls会从tmpInboundBuffer读取数据（BuffReader接口）
	if err != nil {
		return 0, err
	}
	t.d.KeepLastActivity()

	for {
		tlsN, err := t.tlsconn.Read(readBuffer) // 这里其实是把tmpInboundBuffer的数据解密后放到readBuffer内了
		if err != nil {
			if err == tls.ErrDataNotEnough {
				return n, nil
			}
			return n, err
		}
		if tlsN == 0 {
			break
		}
		_, err = t.d.inboundBuffer.Write(readBuffer[:tlsN]) // 再将readBuffer的数据放到inboundBuffer内，然后供上层应用读取
		if err != nil {
			return n, err
		}
	}
	return n, err
}
func (t *TLSConn) BuffReader(needs int) io.Reader {
	return &eofBuff{
		buff:  t.tmpInboundBuffer,
		needs: needs,
	}
}

func (t *TLSConn) BuffWriter() io.Writer {
	return t.d
}

func (t *TLSConn) ID() int64 {
	return t.d.ID()
}
func (t *TLSConn) SetID(id int64) {
	t.d.SetID(id)
}

func (t *TLSConn) UID() string {
	return t.d.UID()
}

func (t *TLSConn) SetUID(uid string) {
	t.d.SetUID(uid)
}

func (t *TLSConn) Fd() NetFd {
	return t.d.Fd()
}

func (t *TLSConn) LocalAddr() net.Addr {
	return t.d.LocalAddr()
}

func (t *TLSConn) RemoteAddr() net.Addr {
	return t.d.RemoteAddr()
}

func (t *TLSConn) SetRemoteAddr(addr net.Addr) {
	t.d.SetRemoteAddr(addr)
}

func (t *TLSConn) Read(b []byte) (int, error) {
	return t.tlsconn.Read(b)
}

func (t *TLSConn) Write(b []byte) (int, error) {
	return t.tlsconn.Write(b)
}

func (t *TLSConn) SetDeadline(tim time.Time) error {
	return t.d.SetDeadline(tim)
}

func (t *TLSConn) SetReadDeadline(tim time.Time) error {
	return t.d.SetReadDeadline(tim)
}

func (t *TLSConn) SetWriteDeadline(tim time.Time) error {
	return t.d.SetWriteDeadline(tim)
}

func (t *TLSConn) Close() error {
	_ = t.tmpInboundBuffer.Release()
	return t.d.Close()
}

func (t *TLSConn) CloseWithErr(err error) error {
	t.tmpInboundBuffer.Release()
	return t.d.CloseWithErr(err)
}

func (t *TLSConn) Context() interface{} {
	return t.d.Context()
}

func (t *TLSConn) SetContext(ctx interface{}) {
	t.d.SetContext(ctx)
}

func (t *TLSConn) WakeWrite() error {
	return t.d.WakeWrite()
}

func (t *TLSConn) Discard(n int) (int, error) {
	return t.d.Discard(n)
}

func (t *TLSConn) InboundBuffer() InboundBuffer {
	return t.d.InboundBuffer()
}

func (t *TLSConn) OutboundBuffer() OutboundBuffer {
	return t.d.OutboundBuffer()
}

func (t *TLSConn) IsAuthed() bool {
	return t.d.IsAuthed()
}

func (t *TLSConn) SetAuthed(authed bool) {
	t.d.SetAuthed(authed)
}

func (t *TLSConn) IsClosed() bool {
	return t.d.IsClosed()
}

func (t *TLSConn) LastActivity() time.Time {
	return t.d.LastActivity()
}

func (t *TLSConn) Peek(n int) ([]byte, error) {
	return t.d.Peek(n)
}

func (t *TLSConn) ReactorSub() *ReactorSub {
	return t.d.ReactorSub()
}

func (t *TLSConn) Flush() error {
	return t.d.Flush()
}

func (t *TLSConn) SetValue(key string, value interface{}) {
	t.d.SetValue(key, value)
}

func (t *TLSConn) Value(key string) interface{} {
	return t.d.Value(key)
}

func (t *TLSConn) Uptime() time.Time {
	return t.d.Uptime()
}

func (t *TLSConn) WriteToOutboundBuffer(b []byte) (int, error) {
	return t.d.outboundBuffer.Write(b)
}

func (t *TLSConn) SetMaxIdle(maxIdle time.Duration) {
	t.d.SetMaxIdle(maxIdle)
}

func (t *TLSConn) String() string {
	return t.d.String()
}

type eofBuff struct {
	buff  InboundBuffer
	needs int
}

func (e *eofBuff) Read(p []byte) (int, error) {
	n, err := e.buff.Read(p)
	e.needs -= n

	if e.needs > 0 && err == ring.ErrIsEmpty {
		return n, tls.ErrDataNotEnough
	}
	if e.needs <= 0 && err == nil {
		return n, io.EOF
	}
	if err != nil {
		if err == ring.ErrIsEmpty {
			return n, io.EOF
		}
		return n, err
	}
	return n, err
}

// func getConnFd(conn net.Conn) (int, error) {
// 	sc, ok := conn.(interface {
// 		SyscallConn() (syscall.RawConn, error)
// 	})
// 	if !ok {
// 		return 0, errors.New("RawConn Unsupported")
// 	}
// 	rc, err := sc.SyscallConn()
// 	if err != nil {
// 		return 0, errors.New("RawConn Unsupported")
// 	}
// 	var newFd int
// 	errCtrl := rc.Control(func(fd uintptr) {
// 		newFd, err = syscall.Dup(int(fd))
// 	})
// 	if errCtrl != nil {
// 		return 0, errCtrl
// 	}
// 	if err != nil {
// 		return 0, err
// 	}

// 	return newFd, nil
// }

type connMatrix struct {
	connCount atomic.Int32
	conns     map[int]Conn
}

func newConnMatrix() *connMatrix {
	return &connMatrix{
		conns: make(map[int]Conn),
	}
}

func (cm *connMatrix) iterate(f func(Conn) bool) {
	for _, c := range cm.conns {
		if c != nil {
			if !f(c) {
				return
			}
		}
	}
}
func (cm *connMatrix) countAdd(delta int32) {
	cm.connCount.Add(delta)
}

func (cm *connMatrix) addConn(c Conn) {
	cm.conns[c.Fd().Fd()] = c
	cm.countAdd(1)
}

func (cm *connMatrix) delConn(c Conn) {
	delete(cm.conns, c.Fd().Fd())
	cm.countAdd(-1)
}

func (cm *connMatrix) getConn(fd int) Conn {
	return cm.conns[fd]
}
func (cm *connMatrix) loadCount() (n int32) {
	return cm.connCount.Load()
}
