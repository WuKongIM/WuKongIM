package wknet

import (
	"bytes"
	"fmt"
	"io"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/wknet/crypto/tls"
	"go.uber.org/zap"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func CreateWSConn(id int64, connFd NetFd, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {
	defaultConn := GetDefaultConn(id, connFd, localAddr, remoteAddr, eg, reactorSub)
	return NewWSConn(defaultConn), nil
}

func CreateWSSConn(id int64, connFd NetFd, localAddr, remoteAddr net.Addr, eg *Engine, reactorSub *ReactorSub) (Conn, error) {
	defaultConn := GetDefaultConn(id, connFd, localAddr, remoteAddr, eg, reactorSub)
	tc := newTLSConn(defaultConn)
	tlsCn := tls.Server(tc, eg.options.WSTLSConfig)
	tc.tlsconn = tlsCn
	return NewWSSConn(tc), nil
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
	n, err := w.fd.Read(readBuffer)
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

func (w *WSConn) WriteServerBinary(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return wsutil.WriteServerBinary(w.outboundBuffer, data)
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
		fmt.Println(" 数据不完整---->", len(buff))
		return nil, nil
	}
	tmpReader := bytes.NewReader(buff)
	header, err := ws.ReadHeader(tmpReader)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF { //数据不完整
			return nil, nil
		}
		fmt.Println(" 发送错误，丢弃数据---->", err)
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
		remLen := tmpReader.Len()
		for tmpReader.Len() > 0 {
			messages, err = wsutil.ReadClientMessage(tmpReader, messages)
			if err != nil {
				w.Warn("read client message error", zap.Error(err))
				break
			}
		}
		remLen = remLen - tmpReader.Len()
		w.DiscardFromTemp(remLen)
		return messages, nil
	} else {
		fmt.Println("header.Fin-->false...", len(buff))
	}
	return nil, nil
}

func (w *WSConn) upgrade() error {
	buff, err := w.PeekFromTemp(-1)
	if err != nil {
		return err
	}
	tmpReader := bytes.NewReader(buff)
	tmpWriter := bytes.NewBuffer(nil)
	_, err = ws.Upgrade(&readWrite{
		Reader: tmpReader,
		Writer: tmpWriter,
	})
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF { //数据不完整
			return nil
		}
		w.DiscardFromTemp(len(buff)) // 发送错误，丢弃数据
		return err
	}
	_, err = w.Write(tmpWriter.Bytes())
	if err != nil {
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
		wsTmpInboundBuffer: tlsConn.d.eg.eventHandler.OnNewInboundConn(tlsConn.d, tlsConn.d.eg), // tls解码后的数据
	}
}

func (w *WSSConn) ReadToInboundBuffer() (int, error) {
	readBuffer := w.d.reactorSub.ReadBuffer
	n, err := w.d.fd.Read(readBuffer)
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
	if len(buff) == 0 {
		return nil
	}

	tmpReader := bytes.NewReader(buff)
	tmpWriter := bytes.NewBuffer(nil)
	_, err = ws.Upgrade(&readWrite{
		Reader: tmpReader,
		Writer: tmpWriter,
	})
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF { //数据不完整
			return nil
		}
		w.discardFromWSTemp(len(buff)) // 发送错误，丢弃数据
		return err
	}
	_, err = w.TLSConn.Write(tmpWriter.Bytes())
	if err != nil {
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
				err = wsutil.HandleClientControlMessage(w.TLSConn, msg)
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

func (w *WSSConn) WriteServerBinary(data []byte) error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return wsutil.WriteServerBinary(w.TLSConn, data)
}

func (w *WSSConn) decode() ([]wsutil.Message, error) {
	buff, err := w.peekFromWSTemp(-1)
	if err != nil {
		return nil, err
	}
	if len(buff) < ws.MinHeaderSize { // 数据不完整
		fmt.Println("数据不完整...")
		return nil, nil
	}
	tmpReader := bytes.NewReader(buff)
	header, err := ws.ReadHeader(tmpReader)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF { //数据不完整
			return nil, nil
		}
		fmt.Println("发送错误，丢弃数据....")
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
		remLen := tmpReader.Len()
		for tmpReader.Len() > 0 {
			messages, err = wsutil.ReadClientMessage(tmpReader, messages)
			if err != nil {
				w.d.Warn("read client message error", zap.Error(err))
				break
			}
		}
		remLen = remLen - tmpReader.Len()
		w.discardFromWSTemp(remLen)
		return messages, nil
	} else {
		fmt.Println("header.Fin-->false...", len(buff))
	}
	return nil, nil
}
