package gateway

import (
	"net"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/logicclient"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

type Gateway struct {
	engine *wknet.Engine
	opts   *Options
	wklog.Log
	monitor     Monitor
	processor   *Processor
	connManager *ConnManager
	timingWheel *timingwheel.TimingWheel // Time wheel delay task
	logic       logicclient.Client
}

func New(opts *Options) *Gateway {
	g := &Gateway{
		monitor:     &emptyMonitor{},
		opts:        opts,
		Log:         wklog.NewWKLog("Gateway"),
		timingWheel: timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		engine:      wknet.NewEngine(wknet.WithAddr(opts.Addr), wknet.WithWSAddr(opts.WSAddr), wknet.WithWSSAddr(opts.WSSAddr), wknet.WithWSTLSConfig(opts.WSTLSConfig)),
	}
	g.processor = NewProcessor(g)
	g.connManager = NewConnManager(g)
	return g
}

func (g *Gateway) Start() error {
	g.engine.OnData(g.onData)
	g.engine.OnConnect(g.onConnect)
	g.engine.OnClose(g.onClose)
	return g.engine.Start()
}

func (g *Gateway) Stop() error {
	err := g.engine.Stop()
	if err != nil {
		g.Warn(err.Error())
	}
	return nil
}

func (g *Gateway) TCPAddr() net.Addr {
	return g.engine.TCPRealListenAddr()
}

func (g *Gateway) SetLogic(logic logicclient.Client) {
	g.logic = logic
}

// Schedule 延迟任务
func (g *Gateway) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return g.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}
