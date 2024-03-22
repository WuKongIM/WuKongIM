package cluster

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type channelGroup struct {
	stopper *syncutil.Stopper
	opts    *Options
	wklog.Log
	listener *ChannelListener
	stopped  atomic.Bool
}

func newChannelGroup(opts *Options) *channelGroup {
	return &channelGroup{
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		Log:      wklog.NewWKLog(fmt.Sprintf("channelGroup[%d]", opts.NodeID)),
		listener: NewChannelListener(opts),
	}
}

func (g *channelGroup) start() error {
	g.stopper.RunWorker(g.listen)
	return g.listener.start()
}

func (g *channelGroup) stop() {
	g.stopped.Store(true)
	g.listener.stop()
	g.stopper.Stop()
}

func (g *channelGroup) add(channel *channel) {
	g.listener.Add(channel)
}

func (g *channelGroup) exist(channelID string, channelType uint8) bool {
	return g.listener.Exist(channelID, channelType)
}

func (g *channelGroup) channel(channelID string, channelType uint8) *channel {
	return g.listener.Get(channelID, channelType)
}

func (g *channelGroup) step(channelID string, channelType uint8, msg replica.Message) error {
	channel := g.listener.Get(channelID, channelType)
	if channel == nil {
		g.Error("channel not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return errors.New("channel not found")
	}
	err := channel.stepLock(msg)
	if err != nil {
		g.Error("channel step error", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Error(err))
		return err
	}
	return nil
}

func (g *channelGroup) listen() {
	for !g.stopped.Load() {
		ready := g.listener.wait()
		if ready.channel == nil {
			continue
		}
		g.handleReady(ready)

	}
}

func (g *channelGroup) handleReady(rd channelReady) {
	var (
		ch      = rd.channel
		shardNo = ch.channelKey()
	)

	if !replica.IsEmptyHardState(rd.HardState) {
		g.Info("设置HardState", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.Uint64("leaderId", rd.HardState.LeaderId), zap.Uint32("term", rd.HardState.Term))
		ch.updateClusterConfigLeaderIdAndTerm(rd.HardState.Term, rd.HardState.LeaderId)
		channelClusterCfg := ch.getClusterConfig()
		err := g.opts.ChannelClusterStorage.Save(ch.channelID, ch.channelType, channelClusterCfg)
		if err != nil {
			g.Panic("save cluster config error", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.Error(err))
		}
	}

	for _, msg := range rd.Messages {
		if msg.To == g.opts.NodeID {
			ch.handleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			g.Error("msg.To is 0", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.String("msg", msg.MsgType.String()))
			continue
		}

		protMsg, err := NewMessage(shardNo, msg, MsgChannelMsg)
		if err != nil {
			g.Error("new message error", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.Error(err))
			continue
		}
		if msg.MsgType != replica.MsgSync && msg.MsgType != replica.MsgSyncResp && msg.MsgType != replica.MsgPing && msg.MsgType != replica.MsgPong {
			g.Info("发送消息", zap.Uint64("id", msg.Id), zap.String("msgType", msg.MsgType.String()), zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.Uint64("to", msg.To), zap.Uint32("term", msg.Term), zap.Uint64("index", msg.Index))
		}

		// trace
		traceOutgoingMessage(trace.ClusterKindChannel, msg)

		// 发送消息
		err = g.opts.Transport.Send(msg.To, protMsg, nil)
		if err != nil {
			g.Warn("send msg error", zap.String("msgType", msg.MsgType.String()), zap.Uint64("to", msg.To), zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.Error(err))
		}
	}

}
