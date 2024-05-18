package server

import (
	"hash/fnv"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/bwmarrin/snowflake"
	"github.com/lni/goutils/syncutil"
	"github.com/pkg/errors"
	"github.com/sasha-s/go-deadlock"
	"go.uber.org/zap"
)

type channelReactor struct {
	messageIDGen       *snowflake.Node     // 消息ID生成器
	processPermissionC chan *permissionReq // 权限请求
	processStorageC    chan *storageReq    // 存储请求
	processDeliverC    chan *deliverReq    // 投递请求
	processSendackC    chan *sendackReq    // 发送回执请求

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
		messageIDGen:       node,
		processPermissionC: make(chan *permissionReq, 2048),
		processStorageC:    make(chan *storageReq, 2048),
		processDeliverC:    make(chan *deliverReq, 2048),
		processSendackC:    make(chan *sendackReq, 2048),
		stopper:            syncutil.NewStopper(),
		opts:               opts,
		Log:                wklog.NewWKLog("Reactor"),
		s:                  s,
	}
	r.subs = make([]*channelReactorSub, r.opts.Reactor.ChannelSubCount)
	for i := 0; i < r.opts.Reactor.ChannelSubCount; i++ {
		sub := newChannelReactorSub(i, r)
		r.subs[i] = sub
	}
	return r
}

func (r *channelReactor) start() error {

	r.stopper.RunWorker(r.processPermissionLoop)
	for i := 0; i < 10; i++ {
		r.stopper.RunWorker(r.processStorageLoop)
	}
	r.stopper.RunWorker(r.processDeliverLoop)
	r.stopper.RunWorker(r.processSendackLoop)

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

func (r *channelReactor) proposeSend(fromUid string, fromDeviceId string, packet *wkproto.SendPacket) error {

	fakeChannelId := packet.ChannelID
	channelType := packet.ChannelType
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(packet.ChannelID, fromUid)
	}

	conn := r.s.userReactor.getConnContext(fromUid, fromDeviceId)
	if conn == nil {
		r.Error("conn not found，not allowed to send message", zap.String("fromUid", fromUid), zap.String("fromDeviceId", fromDeviceId))
		return nil
	}

	decodePayload, err := r.checkAndDecodePayload(packet, conn)
	if err != nil {
		r.Error("Failed to decode payload！", zap.Error(err))
		return err

	}
	packet.Payload = decodePayload

	// 加载或创建频道
	ch := r.loadOrCreateChannel(fakeChannelId, packet.ChannelType)

	// 处理消息
	err = ch.proposeSend(fromUid, fromDeviceId, packet)
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

// decode payload
func (r *channelReactor) checkAndDecodePayload(sendPacket *wkproto.SendPacket, conn *connContext) ([]byte, error) {
	aesKey, aesIV := conn.aesKey, conn.aesIV
	vail, err := r.sendPacketIsVail(sendPacket, conn)
	if err != nil {
		return nil, err
	}
	if !vail {
		return nil, errors.New("sendPacket is illegal！")
	}
	// decode payload
	decodePayload, err := wkutil.AesDecryptPkcs7Base64(sendPacket.Payload, []byte(aesKey), []byte(aesIV))
	if err != nil {
		r.Error("Failed to decode payload！", zap.Error(err))
		return nil, err
	}

	return decodePayload, nil
}

// send packet is vail
func (r *channelReactor) sendPacketIsVail(sendPacket *wkproto.SendPacket, conn *connContext) (bool, error) {
	aesKey, aesIV := conn.aesKey, conn.aesIV
	signStr := sendPacket.VerityString()
	actMsgKey, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		r.Error("msgKey is illegal！", zap.Error(err), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", conn))
		return false, err
	}
	actMsgKeyStr := sendPacket.MsgKey
	exceptMsgKey := wkutil.MD5(string(actMsgKey))
	if actMsgKeyStr != exceptMsgKey {
		r.Error("msgKey is illegal！", zap.String("except", exceptMsgKey), zap.String("act", actMsgKeyStr), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", conn))
		return false, errors.New("msgKey is illegal！")
	}
	return true, nil
}
