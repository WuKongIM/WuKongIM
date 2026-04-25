package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

type ConnConfig struct {
	QueueSizes [numPriorities]int
	Observer   ObserverHooks
}

type rpcResponse struct {
	body []byte
	err  error
}

type MuxConn struct {
	raw        net.Conn
	writer     *priorityWriter
	pending    *pendingMap
	dispatch   func(uint8, []byte, func())
	observer   ObserverHooks
	readerDone chan struct{}
	closeOnce  sync.Once
	closed     atomic.Bool
}

func newMuxConn(raw net.Conn, dispatch func(uint8, []byte, func()), cfg ConnConfig) *MuxConn {
	mc := &MuxConn{
		raw:        raw,
		writer:     newPriorityWriter(raw, cfg.QueueSizes, cfg.Observer),
		pending:    newPendingMap(16),
		dispatch:   dispatch,
		observer:   cfg.Observer,
		readerDone: make(chan struct{}),
	}
	go mc.readLoop()
	return mc
}

func (mc *MuxConn) Send(p Priority, msgType uint8, body []byte) error {
	return mc.writer.enqueue(p, writeItem{msgType: msgType, body: body})
}

func (mc *MuxConn) RPC(ctx context.Context, p Priority, reqID uint64, body []byte) ([]byte, error) {
	ch := make(chan rpcResponse, 1)
	mc.pending.Store(reqID, ch)
	defer mc.pending.Delete(reqID)

	done := make(chan error, 1)
	if err := mc.writer.enqueue(p, writeItem{
		msgType: MsgTypeRPCRequest,
		body:    body,
		done:    done,
	}); err != nil {
		return nil, err
	}

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-mc.readerDone:
		return nil, ErrStopped
	}

	select {
	case resp := <-ch:
		return resp.body, resp.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-mc.readerDone:
		return nil, ErrStopped
	}
}

func (mc *MuxConn) Close() {
	mc.closeOnce.Do(func() {
		mc.closed.Store(true)
		_ = mc.raw.Close()
		mc.writer.stop()
	})
}

func (mc *MuxConn) readLoop() {
	loopErr := io.EOF
	defer mc.closed.Store(true)
	defer close(mc.readerDone)
	defer func() {
		mc.failAllPending(loopErr)
	}()

	for {
		msgType, body, release, err := readFrame(mc.raw)
		if err != nil {
			if mc.closed.Load() {
				loopErr = ErrStopped
			} else {
				loopErr = err
			}
			return
		}
		if hook := mc.observer.OnReceive; hook != nil {
			hook(msgType, len(body))
		}
		if msgType == MsgTypeRPCResponse {
			mc.handleRPCResponse(body)
			release()
			continue
		}
		if mc.dispatch == nil {
			release()
			continue
		}
		mc.dispatch(msgType, body, release)
	}
}

func (mc *MuxConn) handleRPCResponse(body []byte) {
	if len(body) < 9 {
		return
	}
	reqID := binary.BigEndian.Uint64(body[0:8])
	errCode := body[8]
	data := body[9:]

	ch, ok := mc.pending.LoadAndDelete(reqID)
	if !ok {
		return
	}
	var resp rpcResponse
	if errCode != 0 {
		resp.err = fmt.Errorf("nodetransport: remote error: %s", data)
		resp.body = append([]byte(nil), data...)
	} else {
		resp.body = append([]byte(nil), data...)
	}
	ch <- resp
}

func (mc *MuxConn) failAllPending(err error) {
	mc.pending.Range(func(id uint64, ch chan rpcResponse) {
		select {
		case ch <- rpcResponse{err: err}:
		default:
		}
	})
}
