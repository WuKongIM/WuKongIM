package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Channel struct {
	channelID     string
	channelType   uint8
	rc            *replica.Replica      // 副本服务
	destroy       bool                  // 是否已经销毁
	clusterConfig *ChannelClusterConfig // 分布式配置

	maxHandleReadyCountOfBatch int // 每批次处理ready的最大数量

	opts *Options

	lastActivity time.Time // 最后一次活跃时间

	commitWait *commitWait

	doneC chan struct{}

	wklog.Log

	prev *Channel
	next *Channel

	sync.Mutex
}

func NewChannel(clusterConfig *ChannelClusterConfig, appliedIdx uint64, opts *Options) *Channel {
	shardNo := ChannelKey(clusterConfig.ChannelID, clusterConfig.ChannelType)
	rc := replica.New(opts.NodeID, shardNo, replica.WithAppliedIndex(appliedIdx), replica.WithReplicas(clusterConfig.Replicas), replica.WithStorage(newProxyReplicaStorage(shardNo, opts.ShardLogStorage)))
	return &Channel{
		maxHandleReadyCountOfBatch: 50,
		rc:                         rc,
		opts:                       opts,
		Log:                        wklog.NewWKLog(fmt.Sprintf("Channel[%s]", shardNo)),
		commitWait:                 newCommitWait(),
		lastActivity:               time.Now(),
		channelID:                  clusterConfig.ChannelID,
		channelType:                clusterConfig.ChannelType,
		doneC:                      make(chan struct{}),
	}
}

func (c *Channel) Ready() replica.Ready {
	if c.destroy {
		return replica.Ready{}
	}
	c.Lock()
	defer c.Unlock()
	return c.rc.Ready()
}

func (c *Channel) HasReady() bool {
	if c.destroy {
		return false
	}
	c.Lock()
	defer c.Unlock()
	return c.rc.HasReady()
}

// 任命为领导
func (c *Channel) AppointLeader(from uint64, term uint32) error {

	return c.Step(replica.Message{
		MsgType:           replica.MsgAppointLeaderReq,
		From:              from,
		AppointmentLeader: c.opts.NodeID,
		Term:              term,
	})

}

// 任命指定节点为领导
func (c *Channel) AppointLeaderTo(from uint64, term uint32, to uint64) error {
	return c.Step(replica.Message{
		MsgType:           replica.MsgAppointLeaderReq,
		From:              from,
		AppointmentLeader: to,
		Term:              term,
	})
}

func (c *Channel) Step(msg replica.Message) error {
	c.Lock()
	defer c.Unlock()
	return c.step(msg)
}

func (c *Channel) step(msg replica.Message) error {
	if c.destroy {
		return errors.New("channel destroy, can not step")
	}
	c.lastActivity = time.Now()
	return c.rc.Step(msg)
}

func (c *Channel) Propose(data []byte) error {
	if c.destroy {
		return errors.New("channel destroy, can not propose")
	}
	return c.Step(c.rc.NewProposeMessage(data))
}

// 提案数据，并等待数据提交给大多数节点
func (c *Channel) ProposeAndWaitCommit(data []byte, timeout time.Duration) (uint64, error) {

	c.Lock()
	if c.destroy {
		c.Unlock()
		return 0, errors.New("channel destroy, can not propose")
	}
	msg := c.rc.NewProposeMessage(data)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	waitC := c.commitWait.addWaitIndex(msg.Index)
	err := c.step(msg)
	if err != nil {
		c.Unlock()
		return 0, err
	}
	c.Unlock()

	select {
	case <-waitC:
		return msg.Index, nil
	case <-timeoutCtx.Done():
		return 0, timeoutCtx.Err()
	case <-c.doneC:
		return 0, ErrStopped
	}
}

func (c *Channel) ChannelKey() string {
	return ChannelKey(c.channelID, c.channelType)
}

func (c *Channel) Destroy() {
	c.destroy = true
	close(c.doneC)
}

func (c *Channel) IsDestroy() bool {
	return c.destroy
}

// func (c *Channel) Advance() bool {
// 	if c.destroy {
// 		c.Info("channel is destroy, no need to advance")
// 		return false
// 	}
// 	advanceCount := 0
// 	hasAdvance := false
// 	for {
// 		if advanceCount >= c.opts.AdvanceCountOfBatch {
// 			c.Info("reach max handle advance count of batch")
// 			return false
// 		}
// 		advanceCount++
// 		c.takeOutReadyIfNeed()
// 		select {
// 		case rd := <-c.readyC:
// 			for _, msg := range rd.Messages {
// 				c.msgQueue.add(msg)
// 			}
// 			if c.msgQueue.len() > 0 {
// 				c.HandleMsg()
// 			}
// 		case msg := <-c.recvC:
// 			err := c.replica.Step(msg)
// 			if err != nil {
// 				c.Error("step failed", zap.Error(err))
// 			}
// 		case msg := <-c.stepC:
// 			err := c.replica.Step(msg)
// 			if err != nil {
// 				c.Error("step failed", zap.Error(err))
// 			}
// 		case pm := <-c.proposeC:
// 			err := c.replica.Step(pm.msg)
// 			if err != nil {
// 				c.Error("step failed", zap.Error(err))
// 			}
// 			if pm.result != nil {
// 				pm.result <- err
// 				close(pm.result)
// 			}
// 		case <-c.doneC:
// 			return false
// 		default:
// 			c.Info("no need to advance")
// 			goto exit
// 		}
// 		hasAdvance = true
// 	}
// exit:
// 	return hasAdvance
// }

func (c *Channel) LastActivity() time.Time {
	return c.lastActivity
}

// func (c *Channel) PopMessages() []replica.Message {
// 	return c.msgQueue.popAll()
// }

func (c *Channel) HandleLocalMsg(msg replica.Message) {
	if c.destroy {
		c.Warn("handle local msg, but channel is destroy")
		return
	}
	if msg.To != c.opts.NodeID {
		c.Warn("handle local msg, but msg to is not self", zap.Uint64("to", msg.To), zap.Uint64("self", c.opts.NodeID))
		return
	}
	c.lastActivity = time.Now()
	switch msg.MsgType {
	case replica.MsgApplyLogsReq: // 处理apply logs请求
		c.handleApplyLogsReq(msg)
	}
}

// func (c *Channel) HandleMsg() {
// 	var (
// 		err     error
// 		msgs    = c.PopMessages()
// 		shardNo = c.ChannelKey()
// 	)
// 	for _, msg := range msgs {
// 		if msg.To == c.opts.NodeID {
// 			c.handleLocalMsg(msg)
// 			continue
// 		}
// 		if msg.To == 0 {
// 			c.Error("msg to is 0", zap.String("channel", shardNo), zap.String("msgType", msg.MsgType.String()))
// 			continue
// 		}
// 		err = c.opts.Transport.Send(Message{
// 			Message: msg,
// 			ShardNo: shardNo,
// 		})
// 		if err != nil {
// 			c.Error("send message error", zap.Error(err), zap.String("channel", shardNo), zap.String("msgType", msg.MsgType.String()))
// 		}
// 	}
// }

// 处理应用日志请求
func (c *Channel) handleApplyLogsReq(msg replica.Message) {
	if len(msg.Logs) == 0 {
		return
	}
	lastLog := msg.Logs[len(msg.Logs)-1]
	c.Debug("commit wait", zap.Uint64("lastLogIndex", lastLog.Index))
	c.commitWait.commitIndex(lastLog.Index)
	c.Debug("commit wait done", zap.Uint64("lastLogIndex", lastLog.Index))

	err := c.Step(c.rc.NewMsgApplyLogsRespMessage(lastLog.Index))
	if err != nil {
		c.Error("step apply logs resp failed", zap.Error(err))
	}
}
