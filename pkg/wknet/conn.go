package wknet

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/ring"
	"github.com/WuKongIM/WuKongIM/pkg/wknet/crypto/tls"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"

	"go.uber.org/atomic"
	"go.uber.org/zap"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type ConnStats struct {
	InMsgs   *atomic.Int64 // recv msg count
	OutMsgs  *atomic.Int64
	InBytes  *atomic.Int64
	OutBytes *atomic.Int64
}

func NewConnStats() *ConnStats {

	return &ConnStats{
		InMsgs:   atomic.NewInt64(0),
		OutMsgs:  atomic.NewInt64(0),
		InBytes:  atomic.NewInt64(0),
		OutBytes: atomic.NewInt64(0),
	}
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
	DeviceLevel() uint8
	SetDeviceLevel(deviceLevel uint8)
	// DeviceFlag returns the device flag.
	DeviceFlag() uint8
	// SetDeviceFlag sets the device flag.
	SetDeviceFlag(deviceFlag uint8)
	// DeviceID returns the device id.
	DeviceID() string
	// SetValue sets the value associated with key to value.
	SetValue(key string, value interface{})
	// Value returns the value associated with key.
	Value(key string) interface{}
	// SetDeviceID sets the device id.
	SetDeviceID(deviceID string)
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
	// RemoteAddr returns the remote network address.
	RemoteAddr() net.Addr
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
	// ProtoVersion get message proto version
	ProtoVersion() int
	// SetProtoVersion sets message proto version
	SetProtoVersion(version int)
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
	ConnStats() *ConnStats
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
	closed         bool           // if the connection is closed
	isWAdded       bool           // if the connection is added to the write event
	mu             sync.Mutex
	context        interface{}
	authed         bool // if the connection is authed
	protoVersion   int
	id             int64
	uid            string
	deviceFlag     uint8
	deviceLevel    uint8
	deviceID       string
	valueMap       map[string]interface{}
	valueMapLock   sync.RWMutex

	uptime       time.Time
	lastActivity time.Time
	maxIdle      time.Duration
	idleTimer    *timingwheel.Timer

	connStats *ConnStats

	wklog.Log
}

func GetDefaultConn(id int64, connFd NetFd, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) *DefaultConn {
	defaultConn := eg.defaultConnPool.Get().(*DefaultConn)
	defaultConn.id = id
	defaultConn.fd = connFd
	defaultConn.remoteAddr = remoteAddr
	defaultConn.localAddr = localAddr
	defaultConn.isWAdded = false
	defaultConn.authed = false
	defaultConn.closed = false
	defaultConn.uid = ""
	defaultConn.deviceFlag = 0
	defaultConn.deviceLevel = 0
	defaultConn.eg = eg
	defaultConn.reactorSub = reactorSub
	defaultConn.valueMap = map[string]interface{}{}
	defaultConn.context = nil
	defaultConn.uptime = time.Now()
	defaultConn.Log = wklog.NewWKLog(fmt.Sprintf("Conn[[reactor-%d]%d]", reactorSub.idx, id))
	defaultConn.connStats = NewConnStats()

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
	return d.id
}

func (d *DefaultConn) SetID(id int64) {
	d.id = id
}

func (d *DefaultConn) ReadToInboundBuffer() (int, error) {
	readBuffer := d.reactorSub.ReadBuffer
	n, err := d.fd.Read(readBuffer)
	if err != nil || n == 0 {
		return 0, err
	}
	if d.overflowForInbound(n) {
		return 0, fmt.Errorf("inbound buffer overflow, fd: %d currentSize: %d maxSize: %d", d.fd, d.inboundBuffer.BoundBufferSize()+n, d.eg.options.MaxReadBufferSize)
	}
	d.KeepLastActivity()
	_, err = d.inboundBuffer.Write(readBuffer[:n])
	return n, err
}

func (d *DefaultConn) KeepLastActivity() {
	d.lastActivity = time.Now()
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
	if d.closed {
		return -1, net.ErrClosed
	}
	d.mu.Lock()
	defer d.mu.Unlock()
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
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.outboundBuffer.Write(b)

}

func (d *DefaultConn) WakeWrite() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.addWriteIfNotExist()
}

func (d *DefaultConn) IsClosed() bool {

	return d.closed
}

func (d *DefaultConn) Flush() error {

	return d.flush()
}
func (d *DefaultConn) Fd() NetFd {

	return d.fd
}

func (d *DefaultConn) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true

	err := d.reactorSub.DeleteFd(d)
	if err != nil {
		d.Error("delete fd from poller error", zap.Error(err))
	}
	_ = d.fd.Close()
	d.eg.RemoveConn(d)           // remove from the engine
	d.reactorSub.ConnDec()       // decrease the connection count
	d.eg.eventHandler.OnClose(d) // call the close handler
	d.release()

	return nil
}

func (d *DefaultConn) RemoteAddr() net.Addr {

	return d.remoteAddr
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
	d.fd = NetFd{}
	d.maxIdle = 0
	if d.idleTimer != nil {
		d.idleTimer.Stop()
		d.idleTimer = nil
	}
	d.inboundBuffer.Release()
	d.outboundBuffer.Release()

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
	resultData := make([]byte, len(data)) // TODO: 这里考虑用sync.Pool
	copy(resultData, data)                // TODO: 这里需要复制一份，否则多线程下解析数据包会有问题 本人测试 15个连接15个消息 在协程下打印sendPacket的payload会有数据错误问题

	return resultData, nil
}

func (d *DefaultConn) Discard(n int) (int, error) {
	return d.inboundBuffer.Discard(n)
}

func (d *DefaultConn) ReactorSub() *ReactorSub {
	return d.reactorSub
}

func (d *DefaultConn) SetContext(ctx interface{}) {
	d.context = ctx
}
func (d *DefaultConn) Context() interface{} {
	return d.context
}

func (d *DefaultConn) IsAuthed() bool {
	return d.authed
}
func (d *DefaultConn) SetAuthed(authed bool) {
	d.authed = authed
}

func (d *DefaultConn) ProtoVersion() int {

	return d.protoVersion
}
func (d *DefaultConn) SetProtoVersion(version int) {
	d.protoVersion = version
}

func (d *DefaultConn) UID() string {
	return d.uid
}
func (d *DefaultConn) SetUID(uid string) {
	d.uid = uid
}

func (d *DefaultConn) DeviceFlag() uint8 {
	return d.deviceFlag
}

func (d *DefaultConn) SetDeviceFlag(deviceFlag uint8) {
	d.deviceFlag = deviceFlag
}

func (d *DefaultConn) DeviceLevel() uint8 {
	return d.deviceLevel
}

func (d *DefaultConn) SetDeviceLevel(deviceLevel uint8) {
	d.deviceLevel = deviceLevel
}

func (d *DefaultConn) DeviceID() string {
	return d.deviceID
}
func (d *DefaultConn) SetDeviceID(deviceID string) {
	d.deviceID = deviceID
}

func (d *DefaultConn) SetValue(key string, value interface{}) {
	d.valueMapLock.Lock()
	defer d.valueMapLock.Unlock()
	d.valueMap[key] = value
}
func (d *DefaultConn) Value(key string) interface{} {
	d.valueMapLock.RLock()
	defer d.valueMapLock.RUnlock()
	return d.valueMap[key]
}

func (d *DefaultConn) InboundBuffer() InboundBuffer {
	return d.inboundBuffer
}

func (d *DefaultConn) OutboundBuffer() OutboundBuffer {
	return d.outboundBuffer
}

func (d *DefaultConn) LastActivity() time.Time {
	return d.lastActivity
}

func (d *DefaultConn) Uptime() time.Time {
	return d.uptime
}

func (d *DefaultConn) SetMaxIdle(maxIdle time.Duration) {
	d.maxIdle = maxIdle

	if d.idleTimer != nil {
		d.idleTimer.Stop()
	}

	if maxIdle > 0 {
		d.idleTimer = d.eg.Schedule(maxIdle/2, func() {
			if d.lastActivity.Add(maxIdle).After(time.Now()) {
				return
			}
			d.Debug("max idle time exceeded, close the connection", zap.Duration("maxIdle", maxIdle), zap.Duration("lastActivity", time.Since(d.lastActivity)), zap.String("conn", d.String()))
			d.idleTimer.Stop()
			if d.IsClosed() {
				return
			}
			d.Close()
		})
	}
}

func (d *DefaultConn) ConnStats() *ConnStats {
	return d.connStats
}

func (d *DefaultConn) flush() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.outboundBuffer.IsEmpty() {
		d.removeWriteIfExist()
		return nil
	}
	var (
		n   int
		err error
	)

	head, tail := d.outboundBuffer.Peek(-1)
	n, err = d.writeDirect(head, tail)
	_, _ = d.outboundBuffer.Discard(n)
	switch err {
	case nil:
	case syscall.EAGAIN:
		return nil
	default:
		return d.reactorSub.CloseConn(d, os.NewSyscallError("write", err))
	}
	// All data have been drained, it's no need to monitor the writable events,
	// remove the writable event from poller to help the future event-loops.
	if d.outboundBuffer.IsEmpty() {
		err = d.removeWriteIfExist()
		if err != nil {
			fmt.Println("removeWrite err", err)
		}
	} else {
		fmt.Println("outboundBuffer not empty----------------------------------------------------------------------->")
	}
	return nil

}

func (d *DefaultConn) WriteDirect(head, tail []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writeDirect(head, tail)
}

func (d *DefaultConn) writeDirect(head, tail []byte) (int, error) {
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

	// if !d.isWAdded {
	// 	d.isWAdded = true
	// 	// 获取上个调用的方法名
	// 	pc, _, _, _ := runtime.Caller(1)
	// 	d.Debug("add write event", zap.String("uid", d.uid), zap.String("deviceID", d.deviceID), zap.String("caller", runtime.FuncForPC(pc).Name()))
	// 	return d.reactorSub.AddWrite(d)
	// }
	return d.reactorSub.AddWrite(d)
}

func (d *DefaultConn) removeWriteIfExist() error {
	// if d.isWAdded {
	// 	d.isWAdded = false
	// 	return d.reactorSub.RemoveWrite(d)
	// }
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

	return fmt.Sprintf("Conn[%d] uid=%s fd=%d deviceFlag=%s deviceLevel=%s deviceID=%s", d.id, d.uid, d.fd, wkproto.DeviceFlag(d.deviceFlag), wkproto.DeviceLevel(d.deviceLevel), d.deviceID)
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
	return t.d.id
}
func (t *TLSConn) SetID(id int64) {
	t.d.id = id
}

func (t *TLSConn) UID() string {
	return t.d.uid
}

func (t *TLSConn) SetUID(uid string) {
	t.d.uid = uid
}

func (t *TLSConn) Fd() NetFd {
	return t.d.fd
}

func (t *TLSConn) LocalAddr() net.Addr {
	return t.d.LocalAddr()
}

func (t *TLSConn) RemoteAddr() net.Addr {
	return t.d.RemoteAddr()
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
	t.tmpInboundBuffer.Release()
	return t.d.Close()
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

func (t *TLSConn) DeviceFlag() uint8 {
	return t.d.deviceFlag
}

func (t *TLSConn) SetDeviceFlag(flag uint8) {
	t.d.deviceFlag = flag
}

func (t *TLSConn) DeviceLevel() uint8 {
	return t.d.deviceLevel
}

func (t *TLSConn) SetDeviceLevel(level uint8) {
	t.d.deviceLevel = level
}

func (t *TLSConn) DeviceID() string {
	return t.d.deviceID
}
func (t *TLSConn) SetDeviceID(id string) {
	t.d.deviceID = id
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

func (t *TLSConn) ProtoVersion() int {
	return t.d.ProtoVersion()
}

func (t *TLSConn) SetProtoVersion(version int) {
	t.d.SetProtoVersion(version)
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

func (t *TLSConn) ConnStats() *ConnStats {
	return t.d.connStats
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
