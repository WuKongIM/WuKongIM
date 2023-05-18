package wknet

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lio "github.com/WuKongIM/WuKongIM/pkg/wknet/io"
	"golang.org/x/sys/unix"
)

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
	// Write writes the data to the connection.
	// Write(b []byte) (int, error)
	// WriteToOutboundBuffer writes the data to the outbound buffer.
	WriteToOutboundBuffer(b []byte) (int, error)
	// Wake wakes up the connection write.
	WakeWrite() error
	// Fd returns the file descriptor of the connection.
	Fd() int
	// IsClosed returns true if the connection is closed.
	IsClosed() bool
	// Close closes the connection.
	Close() error
	// Release releases the connection.
	Release()
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

	InboundBuffer() InboundBuffer
	OutboundBuffer() OutboundBuffer
}

type DefaultConn struct {
	fd             int
	remoteAddr     net.Addr
	localAddr      net.Addr
	eg             *Engine
	reactorSub     *ReactorSub
	inboundBuffer  InboundBuffer  // inboundBuffer InboundBuffer
	outboundBuffer OutboundBuffer // outboundBuffer OutboundBuffer
	closed         bool           // if the connection is closed
	isWAdded       bool           // if the connection is added to the write event
	wakeWriteLock  sync.RWMutex
	writeLock      sync.RWMutex
	context        interface{}
	authed         bool // if the connection is authed
	protoVersion   int
	id             int64
	uid            string
	deviceFlag     uint8
	deviceLevel    uint8
	deviceID       string
	valueMap       map[string]interface{}
	writeChan      chan []byte

	uptime       time.Time
	lastActivity time.Time

	wklog.Log
}

func CreateConn(id int64, connFd int, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {

	defaultConn := &DefaultConn{
		id:         id,
		fd:         connFd,
		remoteAddr: remoteAddr,
		localAddr:  localAddr,
		eg:         eg,
		reactorSub: reactorSub,
		closed:     false,
		valueMap:   map[string]interface{}{},
		writeChan:  make(chan []byte, 100),
		uptime:     time.Now(),
		Log:        wklog.NewWKLog(fmt.Sprintf("Conn[%d]", id)),
	}
	defaultConn.inboundBuffer = eg.eventHandler.OnNewInboundConn(defaultConn, eg)
	defaultConn.outboundBuffer = eg.eventHandler.OnNewOutboundConn(defaultConn, eg)
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
	n, err := unix.Read(d.fd, readBuffer)
	if err != nil || n == 0 {
		if err == unix.EAGAIN {
			return 0, nil
		}
		return 0, err
	}
	if d.overflowForInbound(n) {
		return 0, fmt.Errorf("inbound buffer overflow, fd: %d currentSize: %d maxSize: %d", d.fd, d.inboundBuffer.BoundBufferSize()+n, d.eg.options.MaxReadBufferSize)
	}
	_, err = d.inboundBuffer.Write(readBuffer[:n])

	d.lastActivity = time.Now()
	return n, err
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
	n, err := d.write(b)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// write to outbound buffer
func (d *DefaultConn) WriteToOutboundBuffer(b []byte) (int, error) {
	d.writeLock.Lock()
	defer d.writeLock.Unlock()
	return d.outboundBuffer.Write(b)
}

func (d *DefaultConn) WakeWrite() error {
	return d.addWriteIfNotExist()
}

func (d *DefaultConn) IsClosed() bool {

	return d.closed
}

func (d *DefaultConn) Flush() error {

	return d.flush()
}
func (d *DefaultConn) Fd() int {

	return d.fd
}

func (d *DefaultConn) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	return syscall.Close(d.fd)
}

func (d *DefaultConn) RemoteAddr() net.Addr {

	return d.remoteAddr
}

func (d *DefaultConn) LocalAddr() net.Addr {
	return d.localAddr
}

func (d *DefaultConn) Release() {
	d.fd = 0
	d.closed = false
	d.isWAdded = false
	d.inboundBuffer.Release()
	d.outboundBuffer.Release()
	d.localAddr = nil
	d.remoteAddr = nil
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

	return d.reactorSub.cache.Bytes(), nil
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
	d.valueMap[key] = value
}
func (d *DefaultConn) Value(key string) interface{} {
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

func (d *DefaultConn) flush() error {

	d.writeLock.Lock()
	defer d.writeLock.Unlock()

	if d.outboundBuffer.IsEmpty() {
		d.removeWriteIfExist()
		return nil
	}
	var (
		n   int
		err error
	)

	head, tail := d.outboundBuffer.Peek(-1)
	if len(head) > 0 && len(tail) > 0 {
		n, err = lio.Writev(d.fd, [][]byte{head, tail})
	} else {
		if len(head) > 0 {
			n, err = unix.Write(d.fd, head)
		} else if len(tail) > 0 {
			n, err = unix.Write(d.fd, tail)
		}
	}
	_, _ = d.outboundBuffer.Discard(n)
	switch err {
	case nil:
	case unix.EAGAIN:
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
	}
	return nil

}

func (d *DefaultConn) write(b []byte) (int, error) {
	n := len(b)
	if n == 0 {
		return 0, nil
	}
	if d.overflowForOutbound(len(b)) { // overflow check
		fmt.Println("overflowForOutbound..................")
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
	d.wakeWriteLock.Lock()
	defer d.wakeWriteLock.Unlock()
	if !d.isWAdded {
		d.isWAdded = true
		return d.reactorSub.AddWrite(d.fd)
	}
	return nil
}

func (d *DefaultConn) removeWriteIfExist() error {
	d.wakeWriteLock.Lock()
	defer d.wakeWriteLock.Unlock()
	if d.isWAdded {
		d.isWAdded = false
		return d.reactorSub.RemoveWrite(d.fd)
	}
	return nil
}

func (d *DefaultConn) overflowForOutbound(n int) bool {
	maxWriteBufferSize := d.eg.options.MaxWriteBufferSize
	return maxWriteBufferSize > 0 && (d.outboundBuffer.BoundBufferSize()+n > maxWriteBufferSize)
}
func (d *DefaultConn) overflowForInbound(n int) bool {
	maxReadBufferSize := d.eg.options.MaxReadBufferSize
	return maxReadBufferSize > 0 && (d.inboundBuffer.BoundBufferSize()+n > maxReadBufferSize)
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
