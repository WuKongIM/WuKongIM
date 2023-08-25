package logic

import (
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type node struct {
	conn           wknet.Conn
	connManager    *clientConnManager
	dataBuffer     *wknet.RingBuffer
	dataBufferLock sync.Mutex

	maxProcessedSeq atomic.Uint64

	proto *proto.Proto

	writeChan chan struct{}

	handeDataing atomic.Bool
	wklog.Log
}

func newNode(conn wknet.Conn) *node {
	connManager := newClientConnManager()
	return &node{
		connManager: connManager,
		conn:        conn,
		dataBuffer:  &wknet.RingBuffer{},
		proto:       proto.New(),
		writeChan:   make(chan struct{}, 100),
		Log:         wklog.NewWKLog(fmt.Sprintf("node[%s]", conn.UID())),
	}
}

func (n *node) nodeID() string {
	return n.conn.UID()
}

func (n *node) connID() int64 {
	return n.conn.ID()
}

func (n *node) addClientConn(conn *clientConn) {
	n.connManager.AddConn(conn)
}

func (n *node) removeClientConn(id int64) {
	n.connManager.RemoveConnWithID(id)
}

func (n *node) getClientConn(id int64) *clientConn {
	return n.connManager.GetConn(id)
}

func (n *node) getAllClientConn() []*clientConn {
	return n.connManager.GetAllConns()
}

func (n *node) reset() {
	n.connManager.Reset()
}

func (n *node) write(data []byte) (int, error) {
	n.dataBufferLock.Lock()
	defer n.dataBufferLock.Unlock()
	count, err := n.dataBuffer.Write(data)
	if err != nil {
		return count, err
	}
	n.writeChan <- struct{}{}
	return count, nil
}

func (n *node) start() {
	go n.loopHandleData()
}

func (n *node) stop() {

}

func (n *node) loopHandleData() {
	ticker := time.NewTicker(time.Second)
	for {

		select {
		case <-ticker.C:
			n.handeData()
		case <-n.writeChan:
			n.handeData()
		}

	}
}

func (n *node) handeData() {
	if n.handeDataing.Load() {
		return
	}
	n.handeDataing.Store(true)
	defer n.handeDataing.Store(false)
	head, tail := n.dataBuffer.Peek(-1)
	if len(head) == 0 && len(tail) == 0 {
		return
	}
	block, size, err := n.proto.Decode(append(head, tail...))
	if err != nil {
		n.Error("decode error", zap.Error(err))
		return
	}

	if block.Seq <= n.maxProcessedSeq.Load() {
		n.Warn("seq is less than maxProcessedSeq", zap.Uint64("seq", block.Seq), zap.Uint64("maxProcessedSeq", n.maxProcessedSeq.Load()))
		return
	}

	_, err = n.dataBuffer.Discard(size)
	if err != nil {
		n.Error("discard error", zap.Error(err))
		return
	}
	n.maxProcessedSeq.Store(block.Seq)
}
