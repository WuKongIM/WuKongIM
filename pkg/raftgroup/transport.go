package raftgroup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/pkg/raftgroup/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	sproto "github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/netutil"
	circuit "github.com/lni/goutils/netutil/rubyist/circuitbreaker"
	"github.com/lni/goutils/syncutil"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrTransportCircuitBreakNotReady  is returned when the transport circuit breaker is not ready.
	ErrTransportCircuitBreakNotReady = errors.New("transport circuit breaker not ready")
	ErrTransportRateLimited          = errors.New("transport rate limited")
)

type ITransport interface {
	Start() error
	Stop()
	// 发送消息
	Send(ctx context.Context, m *RaftMessageReq) error
	// 收到消息
	OnRecvRaftMessage(fnc func(m *RaftMessageReq))
}

type Transport struct {
	nodeID uint64
	addr   string
	mu     struct {
		sync.Mutex
		queues   map[uint64]sendQueue
		breakers map[uint64]*circuit.Breaker
	}
	// maxSendQueueSize 消息发送队大小
	maxSendQueueSize uint64
	sendQueueLength  uint64
	// maxMessageBatchSize 每次节点之间传递的最大消息字节大小
	maxMessageBatchSize uint64

	stopper *syncutil.Stopper
	wklog.Log
	connectionManager IConnectionManager
	server            *wkserver.Server

	onRecv            func(msg *sproto.Message)
	onRecvRaftMessage func(m *RaftMessageReq)
}

func NewTransport(nodeID uint64, addr string, connectionManager IConnectionManager) *Transport {

	if !strings.HasPrefix(addr, "tcp://") {
		addr = "tcp://" + addr
	}
	return &Transport{
		mu: struct {
			sync.Mutex
			queues   map[uint64]sendQueue
			breakers map[uint64]*circuit.Breaker
		}{
			queues:   make(map[uint64]sendQueue),
			breakers: make(map[uint64]*circuit.Breaker),
		},
		maxSendQueueSize:    0,
		sendQueueLength:     1024 * 2,
		maxMessageBatchSize: 64 * 1024 * 1024,
		stopper:             syncutil.NewStopper(),
		Log:                 wklog.NewWKLog("Transport"),
		connectionManager:   connectionManager,
		nodeID:              nodeID,
		addr:                addr,
		server:              wkserver.New(addr),
	}
}

func (t *Transport) Start() error {

	t.server.OnMessage(func(conn wknet.Conn, msg *sproto.Message) {

		if msg.MsgType == MsgTypeRaftMessage {
			raftMessageReq := &RaftMessageReq{}
			err := raftMessageReq.Unmarshal(msg.Content)
			if err != nil {
				t.Error("failed to unmarshal raft message", zap.Error(err))
				return
			}
			if t.onRecvRaftMessage != nil {
				t.onRecvRaftMessage(raftMessageReq)
			}
		} else {
			if t.onRecv != nil {
				t.onRecv(msg)
			}
		}

	})

	err := t.server.Start()
	if err != nil {
		return err
	}
	return nil
}

func (t *Transport) Stop() {
	t.server.Stop()
}

func (t *Transport) Send(ctx context.Context, m *RaftMessageReq) error {

	return t.send(ctx, m)
}

func (t *Transport) send(ctx context.Context, m *RaftMessageReq) error {

	to := m.Message.To
	from := m.Message.From

	// fail fast
	if !t.GetCircuitBreaker(to).Ready() {
		return ErrTransportCircuitBreakNotReady
	}
	t.mu.Lock()
	sq, ok := t.mu.queues[to]
	if !ok {
		sq = sendQueue{
			ch:      make(chan *RaftMessageReq, 1024),
			limiter: wkutil.NewRateLimiter(t.maxSendQueueSize),
		}
		t.mu.queues[to] = sq
	}
	t.mu.Unlock()

	if !ok {
		shutdownQueue := func() {
			t.mu.Lock()
			delete(t.mu.queues, to)
			t.mu.Unlock()
		}
		t.stopper.RunWorker(func() {
			if !t.connectAndProcess(ctx, to, sq, from) {
				t.Error("failed to connect", zap.Uint64("to", to), zap.Uint64("from", from))
			}
			shutdownQueue()
		})

	}
	if sq.rateLimited() {
		return ErrTransportRateLimited
	}
	sq.increase(m)
	select {
	case sq.ch <- m:
		return nil
	default:
		sq.decrease(m)
		return errors.New("send queue is full")
	}
}

func (t *Transport) OnRecv(fnc func(msg *sproto.Message)) {
	t.onRecv = fnc
}

func (t *Transport) OnRecvRaftMessage(fnc func(m *RaftMessageReq)) {
	t.onRecvRaftMessage = fnc
}

func (t *Transport) GetCircuitBreaker(nodeID uint64) *circuit.Breaker {
	t.mu.Lock()
	breaker, ok := t.mu.breakers[nodeID]
	if !ok {
		breaker = netutil.NewBreaker()
		t.mu.breakers[nodeID] = breaker
	}
	t.mu.Unlock()

	return breaker
}

func (t *Transport) connectAndProcess(ctx context.Context, to uint64, sq sendQueue, from uint64) bool {
	breaker := t.GetCircuitBreaker(to) // 熔断器

	conn, err := t.connectionManager.GetConnection(to)
	if err != nil {
		t.Error("failed to get connection", zap.Uint64("to", to), zap.Error(err))
		return false
	}

	err = t.processMessages(ctx, to, sq, conn)
	if err != nil {
		breaker.Fail()
		t.Error("failed to process messages", zap.Uint64("to", to), zap.Error(err))
		return false
	}
	return true
}

func (t *Transport) processMessages(ctx context.Context, nodeID uint64, sq sendQueue, conn IConnection) error {

	idleTimeout := time.Minute
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()
	sz := uint64(0)
	for {
		idleTimer.Reset(idleTimeout)
		select {
		case <-t.stopper.ShouldStop():
			return nil
		case <-idleTimer.C:
			return nil
		case req := <-sq.ch:
			sq.decrease(req)
			sz += uint64(req.Size())

			err := conn.Send(ctx, req)
			if err != nil {
				return err
			}
			for done := false; !done && sz < t.maxMessageBatchSize; {
				select {
				case req := <-sq.ch:
					sq.decrease(req)
					sz += uint64(req.Size())
					err := conn.Send(ctx, req)
					if err != nil {
						return err
					}
				case <-t.stopper.ShouldStop():
					return nil
				default:
					done = true
				}
			}
			err = conn.Flush()
			if err != nil {
				return err
			}
			sz = 0
		}
	}
}

type RaftMessageReq struct {
	ShardID  uint32
	Message  raftpb.Message
	innerReq pb.RaftMessageReq
}

func (r *RaftMessageReq) Marshal() ([]byte, error) {
	r.innerReq.ShardID = r.ShardID
	messageData, err := r.Message.Marshal()
	if err != nil {
		return nil, err
	}
	r.innerReq.Message = messageData
	return proto.Marshal(&r.innerReq)
}

func (r *RaftMessageReq) Unmarshal(data []byte) error {
	err := proto.Unmarshal(data, &r.innerReq)
	if err != nil {
		return err
	}
	r.ShardID = r.innerReq.ShardID
	err = r.Message.Unmarshal(r.innerReq.Message)
	if err != nil {
		return err
	}
	return nil
}

func (r *RaftMessageReq) Size() int {
	return r.Message.Size()
}

type sendQueue struct {
	ch      chan *RaftMessageReq
	limiter *wkutil.RateLimiter
}

func (sq *sendQueue) rateLimited() bool {
	return sq.limiter.RateLimited()
}

func (sq *sendQueue) increase(msg *RaftMessageReq) {
	sq.limiter.Increase(GetEntrySliceInMemSize(msg.Message.Entries))
}

func (sq *sendQueue) decrease(msg *RaftMessageReq) {
	sq.limiter.Decrease(GetEntrySliceInMemSize(msg.Message.Entries))
}

func GetEntrySliceInMemSize(ents []raftpb.Entry) uint64 {
	sz := uint64(0)
	if len(ents) == 0 {
		return 0
	}
	stSz := uint64(unsafe.Sizeof(ents[0]))
	for _, e := range ents {
		sz += uint64(len(e.Data))
		sz += stSz
	}
	return sz
}

type IConnection interface {
	Send(ctx context.Context, m *RaftMessageReq) error
	Flush() error
}

type IConnectionManager interface {
	GetConnection(nodeID uint64) (IConnection, error)
	AddNode(nodeID uint64, addr string)
	RemoveNode(nodeID uint64)
}

type ConnectionManager struct {
	nodeClients map[uint64]*nodeClient
	sync.RWMutex
	nodes map[uint64]string
}

func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		nodeClients: make(map[uint64]*nodeClient),
		nodes:       make(map[uint64]string),
	}
}

func (c *ConnectionManager) GetConnection(nodeID uint64) (IConnection, error) {
	c.Lock()
	defer c.Unlock()
	client, ok := c.nodeClients[nodeID]
	if ok {
		return client, nil
	}
	client = newNodeClient(nodeID, c.nodes[nodeID])
	c.nodeClients[nodeID] = client
	if err := client.connect(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *ConnectionManager) AddNode(nodeID uint64, addr string) {
	c.Lock()
	defer c.Unlock()
	c.nodes[nodeID] = addr
}
func (c *ConnectionManager) RemoveNode(nodeID uint64) {
	c.Lock()
	defer c.Unlock()
	delete(c.nodes, nodeID)
}
