package server

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"go.uber.org/atomic"
	"go.uber.org/zap"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/monitor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/version"
	"github.com/gin-gonic/gin"
	"github.com/judwhite/go-svc"
	"github.com/panjf2000/ants/v2"
)

type stats struct {
	inMsgs      atomic.Int64
	outMsgs     atomic.Int64
	inBytes     atomic.Int64
	outBytes    atomic.Int64
	slowClients atomic.Int64
}

type Server struct {
	stats                                        // 统计信息
	opts                *Options                 // 配置
	wklog.Log                                    // 日志
	handleGoroutinePool *ants.Pool               // 处理逻辑的池
	waitGroupWrapper    *wkutil.WaitGroupWrapper // 协程组
	apiServer           *APIServer               // api服务
	start               time.Time                // 服务开始时间
	timingWheel         *timingwheel.TimingWheel // Time wheel delay task
	deliveryManager     *DeliveryManager         // 消息投递管理
	monitor             monitor.IMonitor         // Data monitoring
	dispatch            *Dispatch                // 消息流入流出分发器
	store               wkstore.Store            // 存储相关接口
	connManager         *ConnManager             // conn manager
	systemUIDManager    *SystemUIDManager        // System uid management, system uid can send messages to everyone without any restrictions
	datasource          IDatasource              // 数据源（提供数据源 订阅者，黑名单，白名单这些数据可以交由第三方提供）
	channelManager      *ChannelManager          // channel manager
	conversationManager *ConversationManager     // conversation manager
	retryQueue          *RetryQueue              // retry queue
	webhook             *Webhook                 // webhook
	monitorServer       *MonitorServer           // 监控服务
	demoServer          *DemoServer              // demo server
	started             bool                     // 服务是否已经启动
	stopChan            chan struct{}            // 服务停止通道

	ipBlacklist     map[string]uint64 // ip黑名单列表
	ipBlacklistLock sync.RWMutex      // ip黑名单列表锁
}

func New(opts *Options) *Server {
	now := time.Now().UTC()
	s := &Server{
		opts:             opts,
		Log:              wklog.NewWKLog("Server"),
		waitGroupWrapper: wkutil.NewWaitGroupWrapper("Server"),
		timingWheel:      timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		start:            now,
		stopChan:         make(chan struct{}),
		ipBlacklist:      map[string]uint64{},
	}

	gin.SetMode(opts.GinMode)

	storeCfg := wkstore.NewStoreConfig()
	storeCfg.DataDir = s.opts.DataDir
	storeCfg.DecodeMessageFnc = func(msg []byte) (wkstore.Message, error) {
		m := &Message{}
		err := m.Decode(msg)
		return m, err
	}

	monitor.SetMonitorOn(opts.Monitor.On) // 监控开关
	s.store = wkstore.NewFileStore(storeCfg)

	s.apiServer = NewAPIServer(s)
	s.deliveryManager = NewDeliveryManager(s)
	s.dispatch = NewDispatch(s)
	s.connManager = NewConnManager(s)
	s.systemUIDManager = NewSystemUIDManager(s)
	s.datasource = NewDatasource(s)
	s.channelManager = NewChannelManager(s)
	s.conversationManager = NewConversationManager(s)
	s.retryQueue = NewRetryQueue(s)
	s.webhook = NewWebhook(s)
	s.monitor = monitor.GetMonitor() // 监控
	s.monitorServer = NewMonitorServer(s)
	s.demoServer = NewDemoServer(s)
	var err error
	s.handleGoroutinePool, err = ants.NewPool(s.opts.HandlePoolSize)
	if err != nil {
		panic(err)
	}

	monitor.SetMonitorOn(s.opts.Monitor.On) // 监控开关

	return s
}

func (s *Server) Init(env svc.Environment) error {
	if env.IsWindowsService() {
		dir := filepath.Dir(os.Args[0])
		return os.Chdir(dir)
	}
	return nil
}

func (s *Server) Start() error {

	fmt.Println(`
	
	__      __       ____  __.                    .___   _____   
	/  \    /  \__ __|    |/ _|____   ____    ____ |   | /     \  
	\   \/\/   /  |  \      < /  _ \ /    \  / ___\|   |/  \ /  \ 
	 \        /|  |  /    |  (  <_> )   |  \/ /_/  >   /    Y    \
	  \__/\  / |____/|____|__ \____/|___|  /\___  /|___\____|__  /
		   \/                \/          \//_____/             \/ 						  
							  
	`)
	s.Info("WuKongIM is Starting...")
	s.Info(fmt.Sprintf("  Using config file:  %s", s.opts.ConfigFileUsed()))
	s.Info(fmt.Sprintf("  Mode:  %s", s.opts.Mode))
	s.Info(fmt.Sprintf("  Version:  %s", version.Version))
	s.Info(fmt.Sprintf("  Git:  %s", fmt.Sprintf("%s-%s", version.CommitDate, version.Commit)))
	s.Info(fmt.Sprintf("  Go build:  %s", runtime.Version()))
	s.Info(fmt.Sprintf("  DataDir:  %s", s.opts.DataDir))

	s.Info(fmt.Sprintf("Listening  for TCP client on %s", s.opts.Addr))
	s.Info(fmt.Sprintf("Listening  for WS client on %s", s.opts.WSAddr))
	if s.opts.WSSAddr != "" {
		s.Info(fmt.Sprintf("Listening  for WSS client on %s", s.opts.WSSAddr))
	}
	s.Info(fmt.Sprintf("Listening  for Manager Http api on %s", fmt.Sprintf("http://%s", s.opts.HTTPAddr)))

	if s.opts.Monitor.On {
		s.Info(fmt.Sprintf("Listening  for Monitor on %s", s.opts.Monitor.Addr))
	}

	defer s.Info("Server is ready")

	err := s.store.Open()
	if err != nil {
		panic(err)
	}
	err = s.dispatch.Start()
	if err != nil {
		panic(err)
	}
	s.apiServer.Start()

	s.conversationManager.Start()
	s.webhook.Start()

	s.retryQueue.Start()

	s.timingWheel.Start()

	s.initIPBlacklist() // 初始化ip黑名单

	// 打印黑名单阻止情况
	s.Schedule(5*time.Minute, func() {
		s.printIpBlacklist()
	})

	if s.opts.Monitor.On {
		s.monitor.Start()
		s.monitorServer.Start()
	}
	if s.opts.Demo.On {
		s.demoServer.Start()
	}

	s.started = true

	return nil
}

func (s *Server) Stop() error {
	s.started = false
	s.Info("Server is Stoping...")

	defer s.Info("Server is exited")

	s.timingWheel.Stop()

	s.retryQueue.Stop()
	_ = s.dispatch.Stop()
	s.apiServer.Stop()
	s.conversationManager.Stop()
	s.webhook.Stop()

	if s.opts.Monitor.On {
		_ = s.monitorServer.Stop()
		s.monitor.Stop()
	}
	if s.opts.Demo.On {
		s.demoServer.Stop()
	}
	s.store.Close()
	close(s.stopChan)

	return nil
}

// Schedule 延迟任务
func (s *Server) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return s.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

func (s *Server) AllowIP(ip string) bool {
	s.ipBlacklistLock.Lock()
	defer s.ipBlacklistLock.Unlock()
	blockCount, ok := s.ipBlacklist[ip]
	if ok {
		s.ipBlacklist[ip] = blockCount + 1
		return false
	}
	return true
}

func (s *Server) AddIPBlacklist(ips []string) {
	s.ipBlacklistLock.Lock()
	defer s.ipBlacklistLock.Unlock()
	for _, ip := range ips {
		s.ipBlacklist[ip] = 0
	}

}

func (s *Server) initIPBlacklist() {
	ips, err := s.store.GetIPBlacklist()
	if err != nil {
		s.Error("获取ip黑名单失败！", zap.Error(err))
		return
	}
	s.ipBlacklistLock.Lock()
	defer s.ipBlacklistLock.Unlock()
	for _, ip := range ips {
		s.ipBlacklist[ip] = 0
	}
}

func (s *Server) RemoveIPBlacklist(ips []string) {
	s.ipBlacklistLock.Lock()
	defer s.ipBlacklistLock.Unlock()
	for _, ip := range ips {
		delete(s.ipBlacklist, ip)
	}
}

func (s *Server) printIpBlacklist() {
	s.ipBlacklistLock.RLock()
	defer s.ipBlacklistLock.RUnlock()
	for ip, count := range s.ipBlacklist {
		if count > 0 {
			s.Info(fmt.Sprintf("ip: %s, block count: %d", ip, count))
		}
	}
}
