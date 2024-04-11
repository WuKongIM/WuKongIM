package cluster

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type channelGroup struct {
	index   int
	stopper *syncutil.Stopper
	opts    *Options
	wklog.Log
	listener *ChannelListener
	stopped  atomic.Bool
	s        *Server
}

func newChannelGroup(index int, s *Server) *channelGroup {
	cg := &channelGroup{
		stopper: syncutil.NewStopper(),
		opts:    s.opts,
		s:       s,
		Log:     wklog.NewWKLog(fmt.Sprintf("channelGroup[%d]", s.opts.NodeID)),
	}
	cg.listener = NewChannelListener(index, cg.handleReady, s.opts)
	return cg
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
		ch = rd.channel
	)
	if !replica.IsEmptyHardState(rd.HardState) {
		g.Debug("设置HardState", zap.String("channelID", ch.channelID), zap.Uint8("channelType", ch.channelType), zap.Uint64("leaderId", rd.HardState.LeaderId), zap.Uint32("term", rd.HardState.Term))
		ch.updateClusterConfigLeaderIdAndTerm(rd.HardState.Term, rd.HardState.LeaderId)
		ch.setLeaderId(rd.HardState.LeaderId)

		err := g.s.defaultPool.Submit(func() {
			g.saveChannelClusterConfig(ch)
		})
		if err != nil {
			g.Error("submit saveChannelClusterConfig failed", zap.Error(err))
		}
	}
	ch.handleReadyMessages(rd.Messages)

}

func (g *channelGroup) saveChannelClusterConfig(channel *channel) {
	channelClusterCfg := channel.getClusterConfig()
	err := g.opts.ChannelClusterStorage.Save(channelClusterCfg)
	if err != nil {
		g.Error("保存channelClusterCfg失败", zap.Error(err))
	}
}
