package cluster

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type ChannelGroup struct {
	stopper *syncutil.Stopper
	opts    *Options
	wklog.Log

	listener *ChannelListener
	stopped  bool
}

func NewChannelGroup(opts *Options) *ChannelGroup {
	return &ChannelGroup{
		stopper:  syncutil.NewStopper(),
		opts:     opts,
		Log:      wklog.NewWKLog(fmt.Sprintf("ChannelGroup[%d]", opts.NodeID)),
		listener: NewChannelListener(opts),
	}
}

func (g *ChannelGroup) Start() error {
	g.stopper.RunWorker(g.listen)
	return g.listener.Start()
}

func (g *ChannelGroup) Stop() {
	g.stopped = true
	g.listener.Stop()
	g.stopper.Stop()
}

func (g *ChannelGroup) Add(channel *Channel) {
	g.listener.Add(channel)
}

func (g *ChannelGroup) Exist(channelID string, channelType uint8) bool {
	return g.listener.Exist(channelID, channelType)
}

func (g *ChannelGroup) Channel(channelID string, channelType uint8) *Channel {
	return g.listener.Get(channelID, channelType)
}

func (g *ChannelGroup) HandleMessage(channelID string, channelType uint8, msg Message) error {
	return g.step(channelID, channelType, msg)
}

func (g *ChannelGroup) step(channelID string, channelType uint8, msg Message) error {
	channel := g.listener.Get(channelID, channelType)
	if channel == nil {
		g.Error("channel not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return errors.New("channel not found")
	}
	err := channel.Step(msg.Message)
	if err != nil {
		g.Error("channel step error", zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Error(err))
		return err
	}
	return nil
}

func (g *ChannelGroup) listen() {
	for !g.stopped {
		ready := g.listener.Wait()
		if ready.channel == nil {
			continue
		}
		g.handleReady(ready)

	}
}

func (g *ChannelGroup) handleReady(rd channelReady) {
	var (
		err     error
		channel = rd.channel
		shardNo = channel.ChannelKey()
	)
	for _, msg := range rd.Messages {
		// g.Info("recv msg", zap.String("msgType", msg.MsgType.String()), zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType), zap.Uint64("to", msg.To))
		if msg.To == g.opts.NodeID {
			channel.HandleLocalMsg(msg)
			continue
		}
		if msg.To == 0 {
			g.Error("msg.To is 0", zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType))
			continue
		}
		err = g.opts.Transport.Send(NewMessage(shardNo, msg))
		if err != nil {
			g.Warn("send msg error", zap.String("channelID", channel.channelID), zap.Uint8("channelType", channel.channelType), zap.Error(err))
		}
	}
}
