package wknet

import (
	"bytes"
	"fmt"
	"io"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/wknet/crypto/tls"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"golang.org/x/sys/unix"
)

func CreateWSConn(id int64, connFd int, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {
	defaultConn := GetDefaultConn(id, connFd, localAddr, remoteAddr, eg, reactorSub)
	if eg.options.WSTLSConfig != nil {
		tc := newTLSConn(defaultConn)
		tlsCn := tls.Server(tc, eg.options.WSTLSConfig)
		tc.tlsconn = tlsCn
		return NewWSSConn(tc), nil
	}
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
	w.KeepLastActivity()

	err = w.unpacketWSData()

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

func (w *WSConn) Close() error {
	w.tmpInboundBuffer.Release()
	return w.DefaultConn.Close()
}

type readWrite struct {
	io.Reader
	io.Writer
}

type WSSConn struct {
	*TLSConn
	upgraded bool

	wsTmpInboundBuffer InboundBuffer // inboundBuffer InboundBuffer
}

func NewWSSConn(tlsConn *TLSConn) *WSSConn {
	return &WSSConn{
		TLSConn:            tlsConn,
		wsTmpInboundBuffer: tlsConn.d.eg.eventHandler.OnNewInboundConn(tlsConn.d, tlsConn.d.eg),
	}
}

func (w *WSSConn) ReadToInboundBuffer() (int, error) {
	readBuffer := w.d.reactorSub.ReadBuffer
	n, err := unix.Read(w.d.fd, readBuffer)
	if err != nil || n == 0 {
		return 0, err
	}
	_, err = w.tmpInboundBuffer.Write(readBuffer[:n])
	if err != nil {
		return 0, err
	}

	for {
		tlsN, err := w.tlsconn.Read(readBuffer)
		if err != nil {
			if err == tls.ErrDataNotEnough {
				return n, nil
			}
			return n, err
		}
		if tlsN == 0 {
			break
		}
		_, err = w.wsTmpInboundBuffer.Write(readBuffer[:tlsN])
		if err != nil {
			return n, err
		}
	}

	w.d.KeepLastActivity()

	err = w.unpacketWSData()

	return n, err
}

func (w *WSSConn) peekFromWSTemp(n int) ([]byte, error) {
	totalLen := w.wsTmpInboundBuffer.BoundBufferSize()
	if n > totalLen {
		return nil, io.ErrShortBuffer
	} else if n <= 0 {
		n = totalLen
	}
	if w.wsTmpInboundBuffer.IsEmpty() {
		return nil, nil
	}
	head, tail := w.wsTmpInboundBuffer.Peek(n)
	w.d.reactorSub.cache.Reset()
	w.d.reactorSub.cache.Write(head)
	w.d.reactorSub.cache.Write(tail)

	data := w.d.reactorSub.cache.Bytes()
	return data, nil
}

func (w *WSSConn) discardFromWSTemp(n int) {
	w.wsTmpInboundBuffer.Discard(n)
}

func (w *WSSConn) upgrade() error {
	buff, err := w.peekFromWSTemp(-1)
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
		w.discardFromWSTemp(len(buff)) // 发送错误，丢弃数据
		return err
	}
	w.discardFromWSTemp(len(buff) - tmpReader.Len())
	w.upgraded = true

	return nil
}

// 解包ws的数据
func (w *WSSConn) unpacketWSData() error {
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
				err = wsutil.HandleClientControlMessage(w, msg)
				if err != nil {
					return err
				}
				continue
			}
			_, err = w.d.inboundBuffer.Write(msg.Payload)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *WSSConn) Close() error {
	w.upgraded = false
	w.wsTmpInboundBuffer.Release()
	return w.TLSConn.Close()
}

func (w *WSSConn) decode() ([]wsutil.Message, error) {
	buff, err := w.peekFromWSTemp(-1)
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
		w.discardFromWSTemp(len(buff)) // 发送错误，丢弃数据
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
		w.discardFromWSTemp(len(buff) - tmpReader.Len())
		return messages, nil
	} else {
		fmt.Println("header.Fin-->false...")
	}
	return nil, nil
}
