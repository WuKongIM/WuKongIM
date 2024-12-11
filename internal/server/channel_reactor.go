package server

import (
	"context"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/bwmarrin/snowflake"
	"github.com/lni/goutils/syncutil"
	"github.com/panjf2000/ants/v2"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type channelReactor struct {
	messageIDGen           *snowflake.Node         // 消息ID生成器
	processInitC           chan *initReq           // 处理频道初始化
	processPayloadDecryptC chan *payloadDecryptReq // 处理消息解密
	processPermissionC     chan *permissionReq     // 权限请求
	processStorageC        chan *storageReq        // 存储请求
	processDeliverC        chan *deliverReq        // 投递请求
	processSendackC        chan *sendackReq        // 发送回执请求
	processForwardC        chan *forwardReq        // 转发请求
	processCloseC          chan *closeReq          // 关闭请求
	processCheckTagC       chan *checkTagReq       // 检查tag请求

	stopper *syncutil.Stopper
	opts    *Options
	s       *Server
	subs    []*channelReactorSub // reactorSub

	wklog.Log

	mu          deadlock.RWMutex
	loadChannMu deadlock.RWMutex

	stopped atomic.Bool

	// 处理者用的携程池
	processGoPool *ants.MultiPool
}

func newChannelReactor(s *Server, opts *Options) *channelReactor {
	node, _ := snowflake.NewNode(int64(opts.Cluster.NodeId))

	r := &channelReactor{
		messageIDGen:           node,
		processInitC:           make(chan *initReq, 2048),
		processPayloadDecryptC: make(chan *payloadDecryptReq, 2048),
		processPermissionC:     make(chan *permissionReq, 2048),
		processStorageC:        make(chan *storageReq, 2048),
		processDeliverC:        make(chan *deliverReq, 2048),
		processSendackC:        make(chan *sendackReq, 2048),
		processForwardC:        make(chan *forwardReq, 2048),
		processCloseC:          make(chan *closeReq, 1024),
		processCheckTagC:       make(chan *checkTagReq, 1024),
		stopper:                syncutil.NewStopper(),
		opts:                   opts,
		Log:                    wklog.NewWKLog(fmt.Sprintf("ChannelReactor[%d]", opts.Cluster.NodeId)),
		s:                      s,
	}
	r.subs = make([]*channelReactorSub, r.opts.Reactor.Channel.SubCount)
	for i := 0; i < r.opts.Reactor.Channel.SubCount; i++ {
		sub := newChannelReactorSub(i, r)
		r.subs[i] = sub
	}

	size := 0
	sizePerPool := 0
	if r.opts.Reactor.Channel.ProcessPoolSize <= r.opts.Reactor.Channel.SubCount {
		size = 1
		sizePerPool = r.opts.Reactor.Channel.ProcessPoolSize
	} else {
		size = r.opts.Reactor.Channel.SubCount
		sizePerPool = r.opts.Reactor.Channel.ProcessPoolSize / r.opts.Reactor.Channel.SubCount
	}
	var err error
	r.processGoPool, err = ants.NewMultiPool(size, sizePerPool, ants.LeastTasks, ants.WithPanicHandler(func(i interface{}) {
		r.Panic("channel reactor processGoPool panic", zap.Any("panic", i), zap.Stack("stack"))
	}))
	if err != nil {
		r.Panic("NewMultiPool panic", zap.Error(err))
	}

	return r
}

func (r *channelReactor) start() error {

	// 高并发处理，适用于分散的耗时任务
	for i := 0; i < 100; i++ {

	}

	// 中并发处理，适合于分散但是不是很耗时的任务
	for i := 0; i < 10; i++ {

	}

	// 低并发处理，适合于集中的耗时任务，这样可以合并请求批量处理
	for i := 0; i < 1; i++ {
		r.stopper.RunWorker(r.processInitLoop)
		r.stopper.RunWorker(r.processPayloadDecryptLoop)
		r.stopper.RunWorker(r.processForwardLoop)
		r.stopper.RunWorker(r.processSendackLoop)
		r.stopper.RunWorker(r.processPermissionLoop)
		r.stopper.RunWorker(r.processStorageLoop)
		r.stopper.RunWorker(r.processDeliverLoop)
		r.stopper.RunWorker(r.processCheckTagLoop)
		r.stopper.RunWorker(r.processCloseLoop)
	}

	for _, sub := range r.subs {
		err := sub.start()
		if err != nil {
			r.Panic("sub start error", zap.Error(err))
		}
	}
	return nil
}

func (r *channelReactor) stop() {

	r.Info("ChannelReactor stop")

	r.stopped.Store(true)

	r.stopper.Stop()

	for _, sub := range r.subs {
		sub.stop()
	}
}

func (r *channelReactor) reactorSub(key string) *channelReactorSub {
	if key == "" {
		r.Panic("reactorSub key is empty")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	h := fnv.New32a()
	h.Write([]byte(key))

	i := h.Sum32() % uint32(len(r.subs))
	return r.subs[i]
}

func (r *channelReactor) proposeSend(messageId int64, fromUid string, fromDeviceId string, fromConnId int64, fromNodeId uint64, isEncrypt bool, packet *wkproto.SendPacket, wait bool) error {

	fakeChannelId := packet.ChannelID
	channelType := packet.ChannelType
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(packet.ChannelID, fromUid)
	}

	// 加载或创建频道
	ch := r.loadOrCreateChannel(fakeChannelId, packet.ChannelType)

	// 处理消息
	err := ch.proposeSend(messageId, fromUid, fromDeviceId, fromConnId, fromNodeId, isEncrypt, packet, wait)
	if err != nil {
		r.Error("proposeSend error", zap.Error(err))
		return err
	}
	return nil
}

func (r *channelReactor) loadOrCreateChannel(fakeChannelId string, channelType uint8) *channel {
	r.loadChannMu.Lock()
	defer r.loadChannMu.Unlock()
	channelKey := wkutil.ChannelToKey(fakeChannelId, channelType)
	sub := r.reactorSub(channelKey)
	ch := sub.channel(channelKey)
	if ch != nil {
		return ch
	}

	ch = newChannel(sub, fakeChannelId, channelType)
	sub.addChannel(ch)
	return ch
}

func (r *channelReactor) WithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.s.ctx, time.Second*10)
}
