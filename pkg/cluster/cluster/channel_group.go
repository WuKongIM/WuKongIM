package cluster

import (
	"errors"
	"fmt"

	replica "github.com/WuKongIM/WuKongIM/pkg/cluster/replica2"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type channelGroup struct {
	stopper *syncutil.Stopper
	opts    *Options
	wklog.Log

	listener *ChannelListener
	stopped  bool
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
	g.stopped = true
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

func (g *channelGroup) handleMessage(channelID string, channelType uint8, msg replica.Message) error {
	return g.step(channelID, channelType, msg)
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
	for !g.stopped {
		ready := g.listener.wait()
		if ready.channel == nil {
			continue
		}
		g.handleReady(ready)

	}
}

func (g *channelGroup) handleReady(rd channelReady) {
	var (
		channel = rd.channel
		shardNo = channel.channelKey()
	)

	for _, msg := range rd.Messages {
		if msg.To == g.opts.NodeID {
			channel.handleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			g.Error("msg.To is 0", zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType))
			continue
		}
		g.Info("send message", zap.String("msgType", msg.MsgType.String()), zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType), zap.Uint64("to", msg.To))

		protMsg, err := NewMessage(shardNo, msg, MsgChannelMsg)
		if err != nil {
			g.Error("new message error", zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType), zap.Error(err))
			continue
		}
		err = g.opts.Transport.Send(msg.To, protMsg)
		if err != nil {
			g.Warn("send msg error", zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType), zap.Error(err))
		}
	}
}
