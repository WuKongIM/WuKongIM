package server

import (
	"hash/fnv"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/bwmarrin/snowflake"
	"github.com/lni/goutils/syncutil"
	"github.com/sasha-s/go-deadlock"
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

	stopper *syncutil.Stopper
	opts    *Options
	s       *Server
	subs    []*channelReactorSub // reactorSub

	wklog.Log

	mu          deadlock.RWMutex
	loadChannMu deadlock.RWMutex
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
		stopper:                syncutil.NewStopper(),
		opts:                   opts,
		Log:                    wklog.NewWKLog("Reactor"),
		s:                      s,
	}
	r.subs = make([]*channelReactorSub, r.opts.Reactor.ChannelSubCount)
	for i := 0; i < r.opts.Reactor.ChannelSubCount; i++ {
		sub := newChannelReactorSub(i, r)
		r.subs[i] = sub
	}
	return r
}

func (r *channelReactor) start() error {

	r.stopper.RunWorker(r.processInitLoop)
	r.stopper.RunWorker(r.processPayloadDecryptLoop)
	r.stopper.RunWorker(r.processPermissionLoop)
	for i := 0; i < 10; i++ {
		r.stopper.RunWorker(r.processStorageLoop)
	}
	r.stopper.RunWorker(r.processDeliverLoop)
	for i := 0; i < 100; i++ {
		r.stopper.RunWorker(r.processSendackLoop)
	}

	for i := 0; i < 100; i++ {
		r.stopper.RunWorker(r.processForwardLoop)
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

	r.stopper.Stop()

	for _, sub := range r.subs {
		sub.stop()
	}
}

func (r *channelReactor) reactorSub(key string) *channelReactorSub {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h := fnv.New32a()
	h.Write([]byte(key))

	i := h.Sum32() % uint32(len(r.subs))
	return r.subs[i]
}

func (r *channelReactor) proposeSend(fromUid string, fromDeviceId string, fromConnId int64, fromNodeId uint64, isEncrypt bool, packet *wkproto.SendPacket) error {

	fakeChannelId := packet.ChannelID
	channelType := packet.ChannelType
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(packet.ChannelID, fromUid)
	}

	// 加载或创建频道
	ch := r.loadOrCreateChannel(fakeChannelId, packet.ChannelType)

	// 处理消息
	err := ch.proposeSend(fromUid, fromDeviceId, fromConnId, fromNodeId, isEncrypt, packet)
	if err != nil {
		r.Error("proposeSend error", zap.Error(err))
		return err
	}
	return nil
}

func (r *channelReactor) loadOrCreateChannel(fakeChannelId string, channelType uint8) *channel {
	r.loadChannMu.Lock()
	defer r.loadChannMu.Unlock()
	channelKey := ChannelToKey(fakeChannelId, channelType)
	sub := r.reactorSub(channelKey)
	ch := sub.channel(channelKey)
	if ch != nil {
		return ch
	}
	ch = newChannel(sub, fakeChannelId, channelType)
	sub.addChannel(ch)
	return ch
}
