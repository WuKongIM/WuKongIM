package gateway

import (
	"net"
	"strings"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/gatewaycommon"
	"github.com/WuKongIM/WuKongIM/internal/logicclient"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
)

type Gateway struct {
	engine *wknet.Engine
	opts   *options.Options
	wklog.Log
	monitor            Monitor
	processor          *Processor
	connManager        *ConnManager
	timingWheel        *timingwheel.TimingWheel // Time wheel delay task
	logicClientManager *logicClientManager
}

func New(opts *options.Options) *Gateway {
	g := &Gateway{
		monitor:     &emptyMonitor{},
		opts:        opts,
		Log:         wklog.NewWKLog("Gateway"),
		timingWheel: timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		engine:      wknet.NewEngine(wknet.WithAddr(opts.Addr), wknet.WithWSAddr(opts.WSAddr), wknet.WithWSSAddr(opts.WSSAddr), wknet.WithWSTLSConfig(opts.WSTLSConfig)),
	}
	g.processor = NewProcessor(g)
	g.connManager = NewConnManager(g)
	g.logicClientManager = newLogicClientManager(g)

	gatewaycommon.SetLocalGatewayServer(g.processor)
	return g
}

func (g *Gateway) Start() error {

	if !g.opts.ClusterOn() {
		logicCli := newLogicClient(logicclient.NewLocalClient(), g)
		g.logicClientManager.addLogicClient(g.opts.LocalID, logicCli)
	} else if strings.TrimSpace(g.opts.Cluster.LogicAddr) != "" {
		logicCli := newLogicClient(logicclient.NewRemoteClient(g.opts.GatewayID(), g.opts.Cluster.LogicAddr, g.opts.Cluster.NodeMaxTransmissionCapacity), g)
		g.logicClientManager.addLogicClient(g.opts.GatewayID(), logicCli)
	}

	g.engine.OnData(g.onData)
	g.engine.OnConnect(g.onConnect)
	g.engine.OnClose(g.onClose)
	return g.engine.Start()
}

func (g *Gateway) Stop() {
	err := g.engine.Stop()
	if err != nil {
		g.Warn(err.Error())
	}
}

func (g *Gateway) TCPAddr() net.Addr {
	return g.engine.TCPRealListenAddr()
}

// Schedule 延迟任务
func (g *Gateway) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return g.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}
