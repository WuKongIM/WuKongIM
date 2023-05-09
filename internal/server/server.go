package server

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
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

	contextPool sync.Pool
	opts        *Options
	wklog.Log
	packetHandler       *PacketHandler           // 包处理器
	handleGoroutinePool *ants.Pool               // 处理逻辑的池
	waitGroupWrapper    *wkutil.WaitGroupWrapper // 协程组
	clientIDGen         atomic.Uint32            // 客户端ID生成
	net                 *Net                     // 长连接服务网络
	clientManager       *ClientManager           // 客户端管理者
	apiServer           *APIServer               // api服务
	start               time.Time                // 服务开始时间
	timingWheel         *timingwheel.TimingWheel // Time wheel delay task
	deliveryManager     *DeliveryManager

	dispatch    *Dispatch
	connManager *ConnManager

	inboundManager *InboundManager
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
	s.contextPool = sync.Pool{
		New: func() any {
			return newContext(s)
		},
	}
	s.apiServer = NewAPIServer(s)
	s.clientManager = NewClientManager(s)
	s.packetHandler = NewPacketHandler(s)
	// s.inboundManager = NewInboundManager(s)
	// s.net = NewNet(s, s)
	s.deliveryManager = NewDeliveryManager(s)
	s.dispatch = NewDispatch(s)
	s.connManager = NewConnManager()
	var err error
	s.handleGoroutinePool, err = ants.NewPool(s.opts.HandlePoolSize)
	if err != nil {
		panic(err)
	}

	return s
}

func (s *Server) Init(env svc.Environment) error {
	if env.IsWindowsService() {
		dir := filepath.Dir(os.Args[0])
		return os.Chdir(dir)
	}
	return nil
}

func (s *Server) GenClientID() uint32 {

	cid := s.clientIDGen.Load()

	if cid >= 1<<32-1 { // 如果超过或等于 int32最大值 这客户端ID从新从0开始生成，int32有几十亿大 如果从1开始生成再回到1 原来属于1的客户端应该早就销毁了。
		s.clientIDGen.Store(0)
	}
	return s.clientIDGen.Inc()
}

func (s *Server) Start() error {
	gitC := gitCommit
	if gitC == "" {
		gitC = "not set"
	}
	fmt.Println(`
	_ _                        
	| (_)                      
	| |_ _ __ ___   __ _  ___  
	| | | '_ ` + "`" + ` _ \ / _` + "`" + ` |/ _ \ 
	| | | | | | | | (_| | (_) |
	|_|_|_| |_| |_|\__,_|\___/ 
							  
							  
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
	s.Info(fmt.Sprintf("Listening  for Websocket client on %s", s.opts.WSSAddr))
	s.Info(fmt.Sprintf("Listening  for Manager Http api on %s", fmt.Sprintf("http://%s", s.opts.HTTPAddr)))

	defer s.Info("Server is ready")

	// s.inboundManager.Start()
	// s.net.Start()
	err = s.dispatch.Start()
	if err != nil {
		panic(err)
	}
	s.apiServer.Start()

	return nil
}

func (s *Server) Stop() error {
	s.Info("Server is Stoping...")

	defer s.Info("Server is exited")

	s.net.Stop()
	s.dispatch.Stop()
	// s.inboundManager.Stop()
	s.apiServer.Stop()
	return nil
}

// Schedule 延迟任务
func (s *Server) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return s.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

func (s *Server) handleFrames(frames []wkproto.Frame, cli *client) {
	frameMap := make(map[wkproto.FrameType][]wkproto.Frame, len(frames)) // TODO: 这里map会增加gc负担，可优化
	for i := 0; i < len(frames); i++ {
		f := frames[i]
		frameList := frameMap[f.GetFrameType()]
		if frameList == nil {
			frameList = make([]wkproto.Frame, 0)
		}
		frameList = append(frameList, f)
		frameMap[f.GetFrameType()] = frameList
	}

	for frameType, frames := range frameMap {
		// ########## 从对象池取出上下文对象并给予新值 ##########
		ctx := s.contextPool.Get().(*limContext)
		ctx.reset()
		ctx.cli = cli
		ctx.frameType = frameType
		ctx.frames = frames
		ctx.proto = s.opts.Proto
		s.handleContext(ctx)
	}
}

func (s *Server) handleContext(ctx *limContext) {
	packetType := ctx.GetFrameType()

	// ########## 统计  ##########
	s.inMsgs.Add(1) // 总流入消息数量
	s.inBytes.Add(int64(ctx.AllFrameSize()))
	cli := ctx.Client()
	if cli != nil {
		cli.Activity()   // 客户端活动了
		cli.inMsgs.Inc() // 客户端收取消息数量+1
		cli.inBytes.Add(int64(ctx.AllFrameSize()))
	}

	switch packetType {
	case wkproto.PING:
		s.packetHandler.handlePing(ctx)
	case wkproto.SEND: //  客户端发送消息包
		s.packetHandler.handleSend(ctx)
	case wkproto.SENDACK: // 客户端收到消息回执
		s.packetHandler.handleRecvack(ctx)
	}
	// ########## 放回对象池 ##########
	s.contextPool.Put(ctx)
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
