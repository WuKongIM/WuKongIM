package wknet

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"golang.org/x/sys/unix"
)

type WSEngine struct {
	e *Engine
	// OnConnect is called when a new connection is established.
	_OnConnect func(conn Conn) error
	// OnData is called when a data is received.
	_OnData func(conn Conn) error
	// OnClose is called when a connection is closed.
	_OnClose func(conn Conn, err error)
}

func NewWSEngine(opts ...Option) *WSEngine {
	ws := &WSEngine{
		e: NewEngine(opts...),
	}
	ws.e.OnNewConn(ws.handleNewConn)
	ws.e.OnConnect(ws.handleConnect)
	ws.e.OnData(ws.handleData)
	ws.e.OnClose(ws.handleClose)
	return ws
}

func (w *WSEngine) OnConnect(onConnect OnConnect) {
	w._OnConnect = onConnect
}
func (w *WSEngine) OnData(onData OnData) {
	w._OnData = onData
}

func (w *WSEngine) OnClose(onClose OnClose) {
	w._OnClose = onClose
}

func (w *WSEngine) handleNewConn(id int64, connFd int, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {
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
	return NewWSConn(defaultConn), nil
}
func (w *WSEngine) handleConnect(conn Conn) error {
	if w._OnConnect == nil {
		return nil
	}
	return w._OnConnect(conn)
}
func (w *WSEngine) handleData(conn Conn) error {

	wsconn := conn.(*WSConn)
	if !wsconn.upgraded {
		err := w.upgrade(wsconn)
		if err != nil {
			return err
		}
		return nil
	}

	messages, err := w.decode(wsconn)
	if err != nil {
		return err
	}
	if len(messages) > 0 {
		for _, msg := range messages {
			if msg.OpCode.IsControl() {
				err = wsutil.HandleClientControlMessage(wsconn, msg)
				if err != nil {
					return err
				}
				continue
			}
			_, err = wsconn.inboundBuffer.Write(msg.Payload)
			if err != nil {
				return err
			}
		}
		if w._OnData != nil {
			return w._OnData(conn)
		}
	}
	return nil
}

func (w *WSEngine) handleClose(conn Conn, err error) {
	if w._OnClose != nil {
		w._OnClose(conn, err)
	}
}

func (w *WSEngine) Start() error {
	return w.e.Start()
}

func (w *WSEngine) Stop() error {
	return w.e.Stop()
}

func (w *WSEngine) upgrade(conn *WSConn) error {
	buff, err := conn.PeekFromTemp(-1)
	if err != nil {
		return err
	}
	tmpReader := bytes.NewReader(buff)
	_, err = ws.Upgrade(&readWrite{
		Reader: tmpReader,
		Writer: conn,
	})
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF { //数据不完整
			return nil
		}
		conn.DiscardFromTemp(len(buff)) // 发送错误，丢弃数据
		return err
	}
	conn.DiscardFromTemp(len(buff) - tmpReader.Len())
	conn.upgraded = true

	return nil
}

func (w *WSEngine) decode(c *WSConn) ([]wsutil.Message, error) {
	buff, err := c.PeekFromTemp(-1)
	if err != nil {
		return nil, err
	}
	if len(buff) < ws.MinHeaderSize { // 数据不完整
		return nil, nil
	}
	tmpReader := bytes.NewReader(buff)
	header, err := ws.ReadHeader(tmpReader)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF { //数据不完整
			return nil, nil
		}
		c.DiscardFromTemp(len(buff)) // 发送错误，丢弃数据
		return nil, err
	}
	dataLen := header.Length
	if dataLen > int64(tmpReader.Len()) { // 数据不完整
		fmt.Println("数据不完整...", dataLen, int64(tmpReader.Len()))
		return nil, nil
	}
	if header.Fin { // 当前 frame 已经是最后一个frame
		var messages []wsutil.Message
		tmpReader.Reset(buff)
		messages, err = wsutil.ReadClientMessage(tmpReader, messages)
		if err != nil {
			return nil, err
		}
		c.DiscardFromTemp(len(buff) - tmpReader.Len())
		return messages, nil
	} else {
		fmt.Println("header.Fin-->false...")
	}
	return nil, nil
}

type WSConn struct {
	*DefaultConn
	upgraded         bool
	tmpInboundBuffer InboundBuffer // inboundBuffer InboundBuffer
}

func NewWSConn(d *DefaultConn) *WSConn {
	w := &WSConn{
		DefaultConn:      d,
		tmpInboundBuffer: d.eg.eventHandler.OnNewInboundConn(d, d.eg),
	}
	return w
}

func (w *WSConn) ReadToInboundBuffer() (int, error) {
	readBuffer := w.reactorSub.ReadBuffer
	n, err := unix.Read(w.fd, readBuffer)
	if err != nil || n == 0 {
		return 0, err
	}
	_, err = w.tmpInboundBuffer.Write(readBuffer[:n])

	w.lastActivity = time.Now()
	return n, err
}
func (w *WSConn) PeekFromTemp(n int) ([]byte, error) {
	totalLen := w.tmpInboundBuffer.BoundBufferSize()
	if n > totalLen {
		return nil, io.ErrShortBuffer
	} else if n <= 0 {
		n = totalLen
	}
	if w.tmpInboundBuffer.IsEmpty() {
		return nil, nil
	}
	head, tail := w.tmpInboundBuffer.Peek(n)
	w.reactorSub.cache.Reset()
	w.reactorSub.cache.Write(head)
	w.reactorSub.cache.Write(tail)

	data := w.reactorSub.cache.Bytes()
	return data, nil
}

// func (w *WSConn) WriteDirect(head, tail []byte) (int, error) {

// 	var (
// 		n   int
// 		err error
// 	)
// 	var data []byte
// 	if len(head) > 0 && len(tail) > 0 {
// 		data = append(head, tail...)
// 	} else {
// 		if len(head) > 0 {
// 			data = head
// 		} else if len(tail) > 0 {
// 			data = tail
// 		}
// 	}
// 	if len(data) > 0 {
// 		bufW := bytes.NewBuffer([]byte{}) // TODO: sync.pool
// 		fmt.Println("write-data--->", len(data))
// 		err = wsutil.WriteServerBinary(bufW, data)
// 		if err != nil {
// 			return 0, err
// 		}
// 		n, err = unix.Write(w.fd, bufW.Bytes())
// 		return n, err
// 	}
// 	return n, err
// }

func (w *WSConn) DiscardFromTemp(n int) {
	w.tmpInboundBuffer.Discard(n)
}

func (w *WSConn) Release() {
	w.DefaultConn.Release()
	w.tmpInboundBuffer.Release()
}

type readWrite struct {
	io.Reader
	io.Writer
}
