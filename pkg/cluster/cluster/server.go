package cluster

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Server struct {
	channelGroupManager  *channelGroupManager  // 频道管理，负责管理频道的集群
	slotManager          *slotManager          // 槽管理 负责管理槽的集群
	nodeManager          *nodeManager          // 节点管理 负责管理节点通信管道
	clusterEventListener *clusterEventListener // 分布式事件监听 用于监听集群事件
	localStorage         *localStorage         // 本地存储 用于存储本地数据
	server               *wkserver.Server      // 分布式通讯服务
	stopper              *syncutil.Stopper
	stopped              bool
	cancelFunc           context.CancelFunc
	cancelCtx            context.Context
	onMessageFnc         func(from uint64, msg *proto.Message) // 上层处理消息的函数
	// 其他
	opts *Options
	wklog.Log
}

func New(nodeId uint64, opts *Options) *Server {

	opts.NodeID = nodeId
	s := &Server{
		slotManager:          newSlotManager(opts),
		nodeManager:          newNodeManager(),
		clusterEventListener: newClusterEventListener(opts),
		stopper:              syncutil.NewStopper(),
		opts:                 opts,
		Log:                  wklog.NewWKLog(fmt.Sprintf("clusterServer[%d]", opts.NodeID)),
	}
	opts.nodeOnlineFnc = s.nodeCliOnline
	opts.requestSlotLogInfo = s.requestSlotLogInfo
	wklog.Configure(&wklog.Options{
		Level: opts.LogLevel,
	})
	s.localStorage = newLocalStorage(s.opts)

	s.channelGroupManager = newChannelGroupManager(s)
	addr := s.opts.Addr
	if !strings.HasPrefix(addr, "tcp://") {
		addr = fmt.Sprintf("tcp://%s", addr)
	}
	s.server = wkserver.New(addr)
	opts.Transport = NewDefaultTransport(s)
	if opts.ShardLogStorage == nil {
		opts.ShardLogStorage = NewPebbleShardLogStorage(path.Join(opts.DataDir, "logdb"))
	}
	s.cancelCtx, s.cancelFunc = context.WithCancel(context.Background())
	return s
}

func (s *Server) nodeCliOnline(nodeID uint64) (bool, error) {
	if s.opts.NodeID == nodeID {
		return true, nil
	}
	node := s.nodeManager.node(nodeID)
	if node == nil {
		return false, errors.New("node not found")
	}
	fmt.Println("online---->", node.online())
	return node.online(), nil
}

func (s *Server) requestSlotLogInfo(ctx context.Context, nodeId uint64, req *SlotLogInfoReq) (*SlotLogInfoResp, error) {
	node := s.nodeManager.node(nodeId)
	if node == nil {
		return nil, ErrNodeNotFound
	}
	return node.requestSlotLogInfo(ctx, req)
}

func (s *Server) Start() error {

	err := s.opts.ShardLogStorage.Open()
	if err != nil {
		return err
	}

	// 接受消息
	s.opts.Transport.OnMessage(func(from uint64, m *proto.Message) {
		s.handleMessage(from, m)
	})

	// 开启通讯服务
	s.server.OnMessage(func(conn wknet.Conn, msg *proto.Message) {
		from := s.nodeIdByServerUid(conn.UID())
		s.opts.Transport.RecvMessage(from, msg)
	})
	s.setRoutes()
	err = s.server.Start()
	if err != nil {
		return err
	}
	// 开启本地存储
	err = s.localStorage.open()
	if err != nil {
		return err
	}

	// 开启分布式事件监听
	nodes := s.clusterEventListener.localAllNodes()
	if len(nodes) > 0 {
		s.addNodes(nodes) // 添加通讯节点
	}
	for nodeID, addr := range s.opts.InitNodes {
		s.addNode(nodeID, addr)
	}

	slots := s.clusterEventListener.localAllSlot()
	if len(slots) > 0 {
		err = s.addSlots(slots) // 添加槽
		if err != nil {
			return err
		}
	}

	err = s.clusterEventListener.start()
	if err != nil {
		return err
	}

	// 开启槽管理
	err = s.slotManager.start()
	if err != nil {
		return err
	}

	// 开启频道管理
	err = s.channelGroupManager.start()
	if err != nil {
		return err
	}

	s.stopper.RunWorker(s.listenClusterEvent)
	return nil
}

func (s *Server) Stop() {
	s.stopped = true
	s.cancelFunc()
	s.server.Stop()
	s.clusterEventListener.stop()
	s.slotManager.stop()
	s.channelGroupManager.stop()
	err := s.localStorage.close()
	if err != nil {
		s.Error("close localStorage is error", zap.Error(err))
	}
	s.stopper.Stop()

	err = s.opts.ShardLogStorage.Close()
	if err != nil {
		s.Error("close ShardLogStorage is error", zap.Error(err))
	}

}

func (s *Server) ServerAPI(route *wkhttp.WKHttp, prefix string) {

	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")

	// route.GET(fmt.Sprintf("%s/channel/clusterinfo", prefix), s.getAllClusterInfo) // 获取所有channel的集群信息
}

// 监听分布式事件
func (s *Server) listenClusterEvent() {
	for !s.stopped {
		event := s.clusterEventListener.wait()
		if !IsEmptyClusterEvent(event) {
			s.handleClusterEvent(event)
		}
	}
}

func (s *Server) SlotIsLeader(slotId uint32) bool {
	st := s.slotManager.slot(slotId)
	if st == nil {
		return false
	}
	return st.isLeader()
}

// 等待leader节点的产生
func (s *Server) WaitNodeLeader(timeout time.Duration) error {
	return s.clusterEventListener.clusterconfigManager.waitNodeLeader(timeout)
}

func (s *Server) handleMessage(from uint64, m *proto.Message) {
	switch m.MsgType {
	case MsgClusterConfigMsg:
		s.handleClusterConfigMsg(from, m)
	case MsgSlotMsg:
		s.handleSlotMsg(from, m)
	case MsgChannelMsg:
		s.handleChannelMsg(from, m)
	default:

		if s.onMessageFnc != nil {
			s.onMessageFnc(from, m)
		}
	}
}

func (s *Server) WaitConfigSlotCount(count uint32, timeout time.Duration) error {
	return s.clusterEventListener.waitConfigSlotCount(count, timeout)
}

func (s *Server) MustWaitConfigSlotCount(count uint32, timeout time.Duration) {
	err := s.WaitConfigSlotCount(count, timeout)
	if err != nil {
		panic(err)
	}
}

func (s *Server) MustWaitAllSlotLeaderReady(timeout time.Duration) {
	err := s.WaitAllSlotLeaderReady(timeout)
	if err != nil {
		s.Panic("WaitAllSlotLeaderReady failed!", zap.Error(err))
	}
}

func (s *Server) WaitAllSlotLeaderReady(timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tick := time.NewTicker(time.Millisecond * 20)
	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if len(s.clusterEventListener.localConfg.Slots) == 0 {
				continue
			}
			all := true
			for _, slot := range s.clusterEventListener.localConfg.Slots {
				if slot.Leader == 0 {
					all = false
					break
				}
			}
			if all {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
	}
}

func (s *Server) Options() *Options {
	return s.opts
}

func (s *Server) handleClusterConfigMsg(from uint64, m *proto.Message) {
	cfgMsg := &clusterconfig.Message{}
	err := cfgMsg.Unmarshal(m.Content)
	if err != nil {
		s.Error("unmarshal cluster config message error", zap.Error(err))
		return
	}
	s.clusterEventListener.handleMessage(*cfgMsg)
}

func (s *Server) handleSlotMsg(from uint64, m *proto.Message) {

	msg, err := UnmarshalMessage(m.Content)
	if err != nil {
		s.Error("unmarshal slot message error", zap.Error(err))
		return
	}
	slotId := GetSlotId(msg.ShardNo, s.Log)
	s.slotManager.handleMessage(slotId, msg.Message)
}

func (s *Server) handleChannelMsg(from uint64, m *proto.Message) {
	msg, err := UnmarshalMessage(m.Content)
	if err != nil {
		s.Error("unmarshal slot message error", zap.Error(err))
		return
	}
	s.Info("收到频道消息", zap.String("msgType", msg.MsgType.String()), zap.Uint64("from", msg.From), zap.Uint32("term", msg.Term), zap.Uint64("to", msg.To), zap.String("shardNo", msg.ShardNo), zap.Uint64("index", msg.Index), zap.Uint64("committed", msg.CommittedIndex))

	channelID, channelType := ChannelFromChannelKey(msg.ShardNo)
	err = s.channelGroupManager.handleMessage(channelID, channelType, msg.Message)
	if err != nil {
		s.Error("handle channel message error", zap.Error(err))
		return
	}
}
