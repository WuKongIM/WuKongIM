package server

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.uber.org/atomic"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/monitor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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
	stats

	opts *Options
	wklog.Log
	handleGoroutinePool *ants.Pool               // 处理逻辑的池
	waitGroupWrapper    *wkutil.WaitGroupWrapper // 协程组
	apiServer           *APIServer               // api服务
	start               time.Time                // 服务开始时间
	timingWheel         *timingwheel.TimingWheel // Time wheel delay task
	deliveryManager     *DeliveryManager
	monitor             monitor.IMonitor // Data monitoring
	dispatch            *Dispatch

	store       wkstore.Store
	connManager *ConnManager // conn manager

	systemUIDManager *SystemUIDManager // System uid management, system uid can send messages to everyone without any restrictions

	datasource          IDatasource          // 数据源（提供数据源 订阅者，黑名单，白名单这些数据可以交由第三方提供）
	channelManager      *ChannelManager      // channel manager
	conversationManager *ConversationManager // conversation manager
	retryQueue          *RetryQueue          // retry queue
	webhook             *Webhook             // webhook
	monitorServer       *MonitorServer       // 监控服务
}

func New(opts *Options) *Server {
	now := time.Now().UTC()
	s := &Server{
		opts:             opts,
		Log:              wklog.NewWKLog("Server"),
		waitGroupWrapper: wkutil.NewWaitGroupWrapper("Server"),
		timingWheel:      timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		start:            now,
	}

	storeCfg := wkstore.NewStoreConfig()
	storeCfg.DataDir = s.opts.DataDir
	storeCfg.DecodeMessageFnc = func(msg []byte) (wkstore.Message, error) {
		m := &Message{}
		err := m.Decode(msg)
		return m, err
	}
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
	gitC := gitCommit
	if gitC == "" {
		gitC = "not set"
	}
	fmt.Println(`
	
	__      __       ____  __.                    .___   _____   
	/  \    /  \__ __|    |/ _|____   ____    ____ |   | /     \  
	\   \/\/   /  |  \      < /  _ \ /    \  / ___\|   |/  \ /  \ 
	 \        /|  |  /    |  (  <_> )   |  \/ /_/  >   /    Y    \
	  \__/\  / |____/|____|__ \____/|___|  /\___  /|___\____|__  /
		   \/                \/          \//_____/             \/ 						  
							  
	`)
	s.Info("Server is Starting...")
	s.Info(fmt.Sprintf("  Mode:  %s", s.opts.Mode))
	s.Info(fmt.Sprintf("  Version:  %s", VERSION))
	s.Info(fmt.Sprintf("  Git:  %s", gitC))
	s.Info(fmt.Sprintf("  Go build:  %s", runtime.Version()))
	s.Info(fmt.Sprintf("  DataDir:  %s", s.opts.DataDir))

	token, new, err := s.generateManagerTokenIfNeed()
	if err != nil {
		panic(err)
	}
	if new {
		s.Info(fmt.Sprintf("  Manager Token:  %s  in %s", token, filepath.Join(s.opts.DataDir, "token")))
	}

	s.Info(fmt.Sprintf("Listening  for TCP client on %s", s.opts.Addr))
	s.Info(fmt.Sprintf("Listening  for Websocket client on %s", s.opts.WSS.Addr))
	s.Info(fmt.Sprintf("Listening  for Manager Http api on %s", fmt.Sprintf("http://%s", s.opts.HTTPAddr)))

	defer s.Info("Server is ready")

	err = s.store.Open()
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

	if s.opts.Monitor.On {
		s.monitor.Start()
		s.monitorServer.Start()
	}

	return nil
}

func (s *Server) Stop() error {
	s.Info("Server is Stoping...")

	defer s.Info("Server is exited")

	s.retryQueue.Stop()

	s.store.Close()

	s.dispatch.Stop()
	s.apiServer.Stop()
	s.conversationManager.Stop()
	s.webhook.Stop()

	if s.opts.Monitor.On {
		s.monitorServer.Stop()
		s.monitor.Stop()
	}

	return nil
}

// Schedule 延迟任务
func (s *Server) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return s.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

// 生成管理token
// 第一个返回参数为超级token的值
// 第二个返回参数为是否新生成的
// 第三个为error
func (s *Server) generateManagerTokenIfNeed() (string, bool, error) {
	file, err := os.OpenFile(filepath.Join(s.opts.DataDir, "token"), os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return "", false, err
	}
	defer file.Close()

	tokenBytes, err := ioutil.ReadAll(file)
	if err != nil {
		return "", false, err
	}
	var token string
	if strings.TrimSpace(string(tokenBytes)) == "" {
		token = wkutil.GenUUID()
		_, err := file.WriteString(token)
		if err != nil {
			return "", false, err
		}
		return token, true, nil
	} else {
		token = string(tokenBytes)
	}
	return token, false, nil
}
