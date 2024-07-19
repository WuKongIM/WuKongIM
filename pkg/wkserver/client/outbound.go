package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type OutBoundWriter interface {
	io.WriteCloser
	SetWriteDeadline(t time.Time) error
}

var (
	outboundStartBufSize           = 512 // For INFO/CONNECT block
	outboundStallClientMinDuration = 100 * time.Millisecond
	outboundStallClientMaxDuration = time.Second

	outboundMinBufSize = 1     // Smallest to shrink to for PING/PONG
	outboundMaxBufSize = 65536 // 64k

	outboundShortsToShrink = 2 // Trigger to shrink dynamic buffers
)

type OutboundOptions struct {
	WriteDeadline time.Duration // 写入超时时间
	MaxPending    int64         // 每个客户端最大等待写出的字节数量 默认为：(64 * 1024 * 1024)=64M
	OnClose       func()        // 关闭连接时的回调
}

func NewOutboundOptions() *OutboundOptions {
	return &OutboundOptions{
		WriteDeadline: time.Second * 10,
		MaxPending:    512 * 1024 * 1024,
	}
}

type outboundFlag uint16

const (
	outboundUnknown outboundFlag = 1 << iota
	outboundWriteLoopStarted
	outboundCloseConnection
	outboundMarkedClosed
	outboundSkipFlushOnClose
	outboundFlushOutbound
)

type OutboundClosedState int

const (
	OutboundClosed = OutboundClosedState(iota + 1)
	OutboundWriteError
	OutboundSlowClientWriteDeadline
	OutboundSlowClientPendingBytes
)

// set the flag (would be equivalent to set the boolean to true)
func (o *outboundFlag) set(c outboundFlag) {
	*o |= c
}

// clear the flag (would be equivalent to set the boolean to false)
func (o *outboundFlag) clear(c outboundFlag) {
	*o &= ^c
}

// isSet returns true if the flag is set, false otherwise
func (o outboundFlag) isSet(c outboundFlag) bool {
	return o&c != 0
}

// setIfNotSet will set the flag `c` only if that flag was not already
// set and return true to indicate that the flag has been set. Returns
// false otherwise.
func (o *outboundFlag) setIfNotSet(c outboundFlag) bool {
	if *o&c == 0 {
		*o |= c
		return true
	}
	return false
}

// 发件箱
type Outbound struct {
	writer                OutBoundWriter
	pendingBytes          []byte // 需要写出去的数据
	secondaryPendingBytes []byte
	writeBuff             net.Buffers   // net.Buffers for writev IO
	startBufCap           int           // 初始化的buff的cap值
	pendingByteCount      atomic.Int64  // 等待写出的字节数量
	pendingMessageCount   atomic.Int64  // 等待写出的消息数量
	flushCond             *sync.Cond    // flush的信道
	shortWrite            atomic.Int64  //  Number of short writes, used for dynamic resizing.
	stallChan             chan struct{} // 这个用于限流的
	lastWriteTime         time.Duration // 最后一次写数据到socket花费时间
	flags                 outboundFlag
	opts                  *OutboundOptions

	mu sync.Mutex

	wklog.Log
}

func NewOutbound(writer OutBoundWriter, opts *OutboundOptions) *Outbound {

	out := &Outbound{
		writer:      writer,
		opts:        opts,
		startBufCap: outboundStartBufSize,
		Log:         wklog.NewWKLog("Outbound"),
	}
	out.flushCond = sync.NewCond(&(out.mu))

	return out
}

func (o *Outbound) Write(data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isClosed() {
		o.Debug("client已关闭不进行写操作！")
		return errors.New("client已关闭不进行写操作！")
	}
	o.queueOutbound(data)

	o.flushSignal()

	if o.stallChan != nil {
		o.stalledWait()
	}

	return nil
}

func (o *Outbound) Flush() {
	o.flushSignal()
}

func (o *Outbound) Start() {
	go o.writeLoop()
}

func (o *Outbound) Close() error {
	o.closeConnection(OutboundClosed)
	return nil
}

func (o *Outbound) IsClosed() bool {
	return o.isClosed()
}

func (o *Outbound) writeLoop() {
	o.mu.Lock()
	if o.isClosed() {
		o.mu.Unlock()
		return
	}
	o.flags.set(outboundWriteLoopStarted)
	o.mu.Unlock()
	waitOk := true
	var closed bool

	for {
		o.mu.Lock()
		if closed = o.isClosed(); !closed {
			if waitOk && (o.pendingByteCount.Load() == 0) {
				o.flushCond.Wait()
				closed = o.isClosed()
			}
		}
		if closed {
			o.flushAndClose()
			o.mu.Unlock()
			o.closeConnection(OutboundWriteError)
			return
		}
		// Flush data
		waitOk = o.flushOutbound()

		o.mu.Unlock()
	}

}

func (o *Outbound) QueueOutbound(data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queueOutbound(data)
}

func (o *Outbound) queueOutbound(data []byte) {
	if o.isClosed() {
		o.Info("连接已关闭，忽略queueOutbound")
		return
	}
	if o.pendingByteCount.Load() > o.opts.MaxPending {
		o.Warn("pending数据超出最大缓存，将断开连接！", zap.Int64("pendingCount", o.pendingByteCount.Load()))
		o.markConnAsClosed(OutboundSlowClientPendingBytes)
		return
	}
	o.pendingByteCount.Add(int64(len(data)))

	if o.pendingBytes == nil && len(data) < outboundMaxBufSize {
		if o.secondaryPendingBytes != nil && cap(o.secondaryPendingBytes) >= o.startBufCap {
			o.pendingBytes = o.secondaryPendingBytes
			o.secondaryPendingBytes = nil
		} else {
			o.pendingBytes = make([]byte, 0, o.startBufCap)
		}
	}
	available := cap(o.pendingBytes) - len(o.pendingBytes)
	if len(data) > available {
		if available > 0 && len(data) < outboundStartBufSize {
			o.pendingBytes = append(o.pendingBytes, data[:available]...)
			data = data[available:]
		}
		// Put the primary on the nb if it has a payload
		if len(o.pendingBytes) > 0 {
			o.writeBuff = append(o.writeBuff, o.pendingBytes)
			o.pendingBytes = nil
		}

		if o.pendingBytes == nil {
			o.growPending(len(data))
		}
	}
	o.pendingBytes = append(o.pendingBytes, data...)

	// 如果待发送的数据大于最大限制的1/2 则将需要做限速。
	if o.pendingByteCount.Load() > o.opts.MaxPending/2 && o.stallChan == nil {
		o.Warn("缓存过大开始限速....", zap.Int("pendingCount", int(o.pendingByteCount.Load())))
		o.stallChan = make(chan struct{})
	}
}

func (o *Outbound) flushOutbound() bool {
	if o.flags.isSet(outboundFlushOutbound) {
		o.mu.Unlock()
		runtime.Gosched()
		o.mu.Lock()
		return false
	}
	o.flags.set(outboundFlushOutbound)
	defer o.flags.clear(outboundFlushOutbound)

	// 如果没有连接或缓存内没数据 则不flush
	if o.writer == nil || o.pendingByteCount.Load() == 0 {
		return true
	}
	nb, attempted := o.collapsePendingToWriterBuff() // 将pending数据放入到writeBuff内
	o.pendingBytes, o.writeBuff, o.secondaryPendingBytes = o.secondaryPendingBytes, nil, nil
	if nb == nil {
		return true
	}
	cnb := nb
	var lfs int
	if len(cnb) > 0 {
		lfs = len(cnb[0])
	}
	writer := o.writer
	pendingMsgCount := o.pendingMessageCount.Load()
	writeDeadline := o.opts.WriteDeadline
	o.mu.Unlock()

	start := time.Now()

	_ = o.writer.SetWriteDeadline(start.Add(writeDeadline))

	n, err := nb.WriteTo(writer) // 真正开始写如socket

	_ = writer.SetWriteDeadline(time.Time{})

	lastWriteTime := time.Since(start)

	o.mu.Lock() // 这里再次锁住 把锁还给调用放，所以调用flushOutbound方法一定要上锁 不上锁 就GG了

	if err != nil && err != io.ErrShortWrite {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			// 写入超时  进入慢写处理逻辑
			o.handleWriteTimeout(n, attempted, len(cnb))
		} else {
			o.Error("Error flushing", zap.Error(err))
			o.mu.Unlock()
			o.markConnAsClosed(OutboundWriteError)
			o.mu.Lock()
			return true
		}
	}

	o.lastWriteTime = lastWriteTime
	o.pendingByteCount.Sub(n)
	o.pendingMessageCount.Sub(pendingMsgCount) // TODO： 这里不一定正确，但是应该大体正确

	if n != attempted && n > 0 {
		o.handlePartialWrite(nb)
	} else if int32(n) >= int32(o.startBufCap) {
		o.shortWrite.Store(0)
	}
	// Adjust based on what we wrote plus any pending.
	pt := n + o.pendingByteCount.Load()

	// 根据short write次数来动态调小pendingBytes的cap 保持2的倍数
	if pt < int64(o.startBufCap) && o.startBufCap > outboundMinBufSize {
		o.shortWrite.Inc()
		if o.shortWrite.Load() > int64(outboundShortsToShrink) {
			o.startBufCap >>= 1
		}
	}
	// Check to see if we can reuse buffers.
	if lfs != 0 && n >= int64(lfs) {
		oldp := cnb[0][:0]
		if cap(oldp) >= o.startBufCap {
			// Replace primary or secondary if they are nil, reusing same buffer.
			if o.pendingBytes == nil {
				o.pendingBytes = oldp
			} else if o.secondaryPendingBytes == nil || cap(o.pendingBytes) < o.startBufCap {
				o.secondaryPendingBytes = oldp
			}
		}
	}
	if o.pendingByteCount.Load() > 0 {
		o.flushSignal()
	}
	// Check if we have a stalled gate and if so and we are recovering release
	// any stalled producers. Only kind==CLIENT will stall.
	if o.stallChan != nil && (n == attempted || o.pendingByteCount.Load() < o.opts.MaxPending/2) {
		close(o.stallChan)
		o.stallChan = nil
	}
	return true

}

func (o *Outbound) growPending(suggestSize int) {
	// Grow here
	if (o.startBufCap << 1) <= outboundMaxBufSize {
		o.startBufCap <<= 1
	}
	if suggestSize > int(o.startBufCap) {
		o.pendingBytes = make([]byte, 0, suggestSize)
	} else {
		if o.secondaryPendingBytes != nil && cap(o.secondaryPendingBytes) >= o.startBufCap {
			o.pendingBytes = o.secondaryPendingBytes
			o.secondaryPendingBytes = nil
		} else {
			o.pendingBytes = make([]byte, 0, o.startBufCap)
		}
	}
}

func (o *Outbound) isClosed() bool {
	return o.flags.isSet(outboundCloseConnection) || o.flags.isSet(outboundMarkedClosed)
}

// 调用此方法需要加锁
func (o *Outbound) flushAndClose() {
	if !o.flags.isSet(outboundSkipFlushOnClose) && o.pendingByteCount.Load() > 0 {
		o.flushOutbound()
	}
	o.pendingBytes, o.secondaryPendingBytes = nil, nil
	if o.writer != nil {
		o.writer.Close()
		o.writer = nil
	}
	if o.opts.OnClose != nil {
		o.opts.OnClose()
	}

}

func (o *Outbound) closeConnection(reason OutboundClosedState) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.flags.isSet(outboundCloseConnection) {
		return
	}
	o.flags.set(outboundCloseConnection)
	o.markConnAsClosed(reason)

	if o.stallChan != nil {
		close(o.stallChan)
		o.stallChan = nil
	}
}

// 调用此方法需要加锁
func (o *Outbound) markConnAsClosed(reason OutboundClosedState) {
	switch reason {
	case OutboundWriteError, OutboundSlowClientWriteDeadline, OutboundSlowClientPendingBytes:
		o.flags.set(outboundSkipFlushOnClose)
	}
	if o.flags.isSet(outboundMarkedClosed) {
		return
	}
	o.flags.set(outboundMarkedClosed)

	if o.flags.isSet(outboundWriteLoopStarted) {
		o.flushSignal()
		return
	}
	o.flushAndClose()

}

func (o *Outbound) flushSignal() {
	o.flushCond.Signal()
}
func (o *Outbound) stalledWait() {
	stall := o.stallChan
	ttl := stallDuration(o.pendingByteCount.Load(), o.opts.MaxPending)
	o.mu.Unlock()
	defer o.mu.Lock()

	select {
	case <-stall:
	case <-time.After(ttl):
		o.Debug(fmt.Sprintf("Timed out of fast producer stall (%v)", ttl))
	}
}
func stallDuration(pb, mp int64) time.Duration {
	ttl := outboundStallClientMinDuration
	if pb >= mp {
		ttl = outboundStallClientMaxDuration
	} else if hmp := mp / 2; pb > hmp {
		bsz := hmp / 10
		additional := int64(ttl) * ((pb - hmp) / bsz)
		ttl += time.Duration(additional)
	}
	return ttl
}

func (o *Outbound) collapsePendingToWriterBuff() (net.Buffers, int64) {

	if o.pendingBytes != nil {
		p := o.pendingBytes
		o.pendingBytes = nil
		return append(o.writeBuff, p), o.pendingByteCount.Load()
	}
	return o.writeBuff, o.pendingByteCount.Load()
}

func (o *Outbound) handleWriteTimeout(written, attempted int64, numChunks int) {
	o.Warn("发现写入慢的客户端", zap.Duration("writeDeadline", o.opts.WriteDeadline), zap.Int("writeSize", numChunks), zap.Int64("totalSize", attempted))
	o.markConnAsClosed(OutboundSlowClientWriteDeadline)
}
func (o *Outbound) handlePartialWrite(pnb net.Buffers) {
	nb, _ := o.collapsePendingToWriterBuff()
	// The partial needs to be first, so append nb to pnb
	o.writeBuff = append(pnb, nb...)
}
