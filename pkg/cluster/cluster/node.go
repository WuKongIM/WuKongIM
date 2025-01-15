package cluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type node struct {
	id     uint64
	addr   string
	client *client.Client

	sendQueue sendQueue

	stopper             *syncutil.Stopper
	maxMessageBatchSize uint64 // 每次发送消息的最大大小（单位字节）
	wklog.Log
	opts *Options
}

func newNode(id uint64, uid string, addr string, opts *Options) *node {

	n := &node{
		id:                  id,
		addr:                addr,
		opts:                opts,
		stopper:             syncutil.NewStopper(),
		maxMessageBatchSize: opts.MaxMessageBatchSize,
		Log:                 wklog.NewWKLog(fmt.Sprintf("nodeClient[%d]", id)),
		sendQueue: sendQueue{
			ch: make(chan *proto.Message, opts.SendQueueLength),
			rl: NewRateLimiter(opts.MaxSendQueueSize),
		},
	}
	n.client = client.New(addr, client.WithUid(uid))
	return n
}

func (n *node) start() {
	n.stopper.RunWorker(n.processMessages)

	err := n.client.Start()
	if err != nil {
		n.Panic("client start failed", zap.Error(err))
	}
}

func (n *node) stop() {
	n.stopper.Stop()
	n.client.Stop()
}

func (n *node) send(msg *proto.Message) error {
	if n.sendQueue.rateLimited() { // 发送队列限流
		n.Error("sendQueue is rateLimited")
		return ErrRateLimited
	}
	n.sendQueue.increase(msg)

	select {
	case n.sendQueue.ch <- msg:
		return nil
	default:
		n.sendQueue.decrease(msg)
		n.Error("sendQueue is full", zap.Int("length", len(n.sendQueue.ch)), zap.Uint32("msgType", msg.MsgType))
		return ErrChanIsFull
	}
}

// 请求中的数量
func (n *node) requesting() int64 {
	if n.client != nil {
		return n.client.Requesting.Load()
	}
	return 0
}

// 消息发送中的数量
func (n *node) sending() int64 {
	return n.sendQueue.count.Load()
}

func (n *node) processMessages() {
	size := uint64(0)
	msgs := make([]*proto.Message, 0)
	var err error
	for {
		select {
		case msg := <-n.sendQueue.ch:

			if !n.client.IsAuthed() {
				continue
			}

			if n.client.Options().LogDetailOn {
				n.Info("send message", zap.Uint32("msgType", msg.MsgType))
			}

			n.sendQueue.decrease(msg)
			size += uint64(msg.Size())
			msgs = append(msgs, msg)

			// 取出所有消息并取出的消息总大小不超过maxMessageBatchSize
			for done := false; !done && size < n.maxMessageBatchSize; {
				select {
				case msg = <-n.sendQueue.ch:
					n.sendQueue.decrease(msg)
					size += uint64(msg.Size())
					msgs = append(msgs, msg)
				case <-n.stopper.ShouldStop():
					return
				default:
					done = true
				}
			}
			if err = n.sendBatch(msgs); err != nil {
				if n.client.IsAuthed() { // 只有连接状态下才打印错误日志
					n.Error("sendBatch is failed", zap.Error(err))
				}
			}
			size = 0
			msgs = msgs[:0]
		case <-n.stopper.ShouldStop():
			return
		}
	}
}

func (n *node) sendBatch(msgs []*proto.Message) error {
	for _, msg := range msgs {
		if err := n.client.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (n *node) requestWithContext(ctx context.Context, path string, body []byte) (*proto.Response, error) {
	return n.client.RequestWithContext(ctx, path, body)
}

type sendQueue struct {
	ch    chan *proto.Message
	rl    *RateLimiter
	count atomic.Int64
}

func (sq *sendQueue) rateLimited() bool {
	return sq.rl.RateLimited()
}

func (sq *sendQueue) increase(msg *proto.Message) {
	size := msg.Size()
	sq.rl.Increase(uint64(size))
	sq.count.Inc()
}

func (sq *sendQueue) decrease(msg *proto.Message) {
	size := msg.Size()
	sq.count.Dec()
	sq.rl.Decrease(uint64(size))
}
