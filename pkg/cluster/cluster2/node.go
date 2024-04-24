package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/netutil"
	circuit "github.com/lni/goutils/netutil/rubyist/circuitbreaker"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type node struct {
	id              uint64
	addr            string
	client          *client.Client
	activityTimeout time.Duration // 活动超时时间，如果这个时间内没有活动，就表示节点已下线

	breaker             *circuit.Breaker
	sendQueue           sendQueue
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
		activityTimeout:     time.Minute * 2, // TODO: 这个时间也不能太短，如果太短节点可能在启动中，这时可能认为下线了，导致触发领导的转移
		breaker:             netutil.NewBreaker(),
		stopper:             syncutil.NewStopper(),
		maxMessageBatchSize: opts.MaxMessageBatchSize,
		Log:                 wklog.NewWKLog(fmt.Sprintf("nodeClient[%d]", id)),
		sendQueue: sendQueue{
			ch: make(chan *proto.Message, opts.SendQueueLength),
			rl: NewRateLimiter(opts.MaxSendQueueSize),
		},
	}
	n.client = client.New(addr, client.WithUID(uid), client.WithOnConnectStatus(n.connectStatusChange), client.WithRequestTimeout(opts.ReqTimeout))
	return n
}

func (n *node) connectStatusChange(status client.ConnectStatus) {
	// n.Debug("节点连接状态改变", zap.String("status", status.String()))
}

func (n *node) start() {
	n.stopper.RunWorker(n.processMessages)

	n.client.SetActivity(time.Now())
	n.client.Start()

}

func (n *node) stop() {
	n.Debug("close")
	n.stopper.Stop()
	n.Debug("close1")
	n.client.Close()
	n.Debug("close2")
}

func (n *node) send(msg *proto.Message) error {
	if !n.breaker.Ready() { // 断路器，防止雪崩
		return errCircuitBreakerNotReady
	}
	if n.sendQueue.rateLimited() { // 发送队列限流
		return errRateLimited
	}
	n.sendQueue.increase(msg)

	select {
	case n.sendQueue.ch <- msg:
		return nil
	default:
		n.sendQueue.decrease(msg)
		return errChanIsFull
	}
}

func (n *node) outboundFlightMessageCount() int64 {
	return n.sendQueue.count.Load()
}

func (n *node) outboundFlightMessageBytes() int64 {
	return int64(n.sendQueue.rl.Get())
}

func (n *node) processMessages() {

	size := uint64(0)
	msgs := make([]*proto.Message, 0)
	var err error
	for {
		select {
		case msg := <-n.sendQueue.ch:

			if n.client.ConnectStatus() != client.CONNECTED {
				continue
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
			trace.GlobalTrace.Metrics.Cluster().MessageOutgoingBytesAdd(int64(size))
			trace.GlobalTrace.Metrics.Cluster().MessageOutgoingCountAdd(int64(len(msgs)))

			if err = n.sendBatch(msgs); err != nil {
				if n.client.ConnectStatus() == client.CONNECTED { // 只有连接状态下才打印错误日志
					n.Error("sendBatch is failed", zap.Error(err))
				}
			}
			size = 0
			msgs = msgs[:0]
			time.Sleep(time.Millisecond * 2)
		case <-n.stopper.ShouldStop():
			return
		}
	}
}

func (n *node) sendBatch(msgs []*proto.Message) error {
	return n.client.SendBatch(msgs)
}

func (n *node) requestWithContext(ctx context.Context, path string, body []byte) (*proto.Response, error) {
	return n.client.RequestWithContext(ctx, path, body)
}

// requestChannelLastLogInfo 请求channel的最后一条日志信息
func (n *node) requestChannelLastLogInfo(ctx context.Context, req ChannelLastLogInfoReqSet) (ChannelLastLogInfoResponseSet, error) {
	fmt.Println("requestChannelLastLogInfo....")
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/lastloginfo", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestChannelLastLogInfo is failed, status:%d", resp.Status)
	}
	channelLastLogInfoResp := ChannelLastLogInfoResponseSet{}
	err = channelLastLogInfoResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return channelLastLogInfoResp, nil
}

func (n *node) requestChannelClusterConfig(ctx context.Context, req *ChannelClusterConfigReq) (wkdb.ChannelClusterConfig, error) {
	data, err := req.Marshal()
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/clusterconfig", data)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}
	if resp.Status != proto.Status_OK {
		return wkdb.EmptyChannelClusterConfig, fmt.Errorf("requestChannelClusterConfig is failed,nodeId: %d status:%d err:%s", n.id, resp.Status, []byte(resp.Body))
	}
	channelClusterConfigResp := wkdb.ChannelClusterConfig{}
	err = channelClusterConfigResp.Unmarshal(resp.Body)
	if err != nil {
		return wkdb.EmptyChannelClusterConfig, err
	}
	return channelClusterConfigResp, nil
}

func (n *node) requestChannelProposeMessage(ctx context.Context, req *ChannelProposeReq) (*ChannelProposeResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/channel/proposeMessage", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		if len(resp.Body) > 0 {
			return nil, errors.New(string(resp.Body))
		}
		return nil, fmt.Errorf("requestProposeMessage is failed, status:%d", resp.Status)
	}
	proposeMessageResp := &ChannelProposeResp{}
	err = proposeMessageResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return proposeMessageResp, nil
}

// 请求更新节点api地址
func (n *node) requestUpdateNodeApiServerAddr(ctx context.Context, req *UpdateApiServerAddrReq) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/config/proposeUpdateApiServerAddr", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestUpdateNodeApiServerAddr is failed, status:%d", resp.Status)
	}
	return nil
}

func (n *node) requestSlotLogInfo(ctx context.Context, req *SlotLogInfoReq) (*SlotLogInfoResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/logInfo", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestSlotLogInfo is failed, status:%d", resp.Status)
	}
	slotLogInfoResp := &SlotLogInfoResp{}
	err = slotLogInfoResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return slotLogInfoResp, nil

}

func (n *node) requestSlotPropose(ctx context.Context, req *SlotProposeReq) (*SlotProposeResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/propose", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestSlotPropose is failed, status:%d", resp.Status)
	}
	proposeMessageResp := &SlotProposeResp{}
	err = proposeMessageResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return proposeMessageResp, nil
}

func (n *node) requestClusterJoin(ctx context.Context, req *ClusterJoinReq) (*ClusterJoinResp, error) {
	data, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	resp, err := n.client.RequestWithContext(ctx, "/cluster/join", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("requestClusterJoin is failed, status:%d", resp.Status)
	}
	clusterJoinResp := &ClusterJoinResp{}
	err = clusterJoinResp.Unmarshal(resp.Body)
	return clusterJoinResp, err
}

func (n *node) requestSlotMigrateFinished(ctx context.Context, req *SlotMigrateFinishReq) error {
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := n.client.RequestWithContext(ctx, "/slot/migrate/finish", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("requestSlotMigrateFinished is failed, status:%d", resp.Status)
	}
	return nil
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
