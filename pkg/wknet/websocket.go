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

func CreateWSConn(id int64, connFd int, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {
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
	if err != nil {
		return 0, err
	}
	err = w.unpacketWSData()
	w.lastActivity = time.Now()
	return n, err
}

// 解包ws的数据
func (w *WSConn) unpacketWSData() error {

	if !w.upgraded {
		err := w.upgrade()
		if err != nil {
			return err
		}
		return nil
	}

	messages, err := w.decode()
	if err != nil {
		return err
	}
	if len(messages) > 0 {
		for _, msg := range messages {
			if msg.OpCode.IsControl() {
				fmt.Println("controler--->")
				err = wsutil.HandleClientControlMessage(w, msg)
				if err != nil {
					return err
				}
				continue
			}
			_, err = w.inboundBuffer.Write(msg.Payload)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *WSConn) decode() ([]wsutil.Message, error) {
	buff, err := w.PeekFromTemp(-1)
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
		w.DiscardFromTemp(len(buff)) // 发送错误，丢弃数据
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
		w.DiscardFromTemp(len(buff) - tmpReader.Len())
		return messages, nil
	} else {
		fmt.Println("header.Fin-->false...")
	}
	return nil, nil
}

func (w *WSConn) upgrade() error {
	buff, err := w.PeekFromTemp(-1)
	if err != nil {
		return err
	}
	tmpReader := bytes.NewReader(buff)
	_, err = ws.Upgrade(&readWrite{
		Reader: tmpReader,
		Writer: w,
	})
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF { //数据不完整
			return nil
		}
		w.DiscardFromTemp(len(buff)) // 发送错误，丢弃数据
		return err
	}
	w.DiscardFromTemp(len(buff) - tmpReader.Len())
	w.upgraded = true

	return nil
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
