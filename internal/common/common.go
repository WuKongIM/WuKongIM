package common

import (
	"fmt"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/errors"
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkcache"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// CommonService 通用服务
type Service struct {
	client *ingress.Client
	wklog.Log
	timingWheel *timingwheel.TimingWheel
}

func NewService() *Service {

	return &Service{
		client:      ingress.NewClient(),
		Log:         wklog.NewWKLog("common.Service"),
		timingWheel: timingwheel.NewTimingWheel(options.G.TimingWheelTick, options.G.TimingWheelSize),
	}
}

func (s *Service) Start() error {
	s.timingWheel.Start()
	return nil
}

func (s *Service) Stop() {
	s.timingWheel.Stop()
}

// Schedule 延迟任务
func (s *Service) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return s.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

func (s *Service) AfterFunc(d time.Duration, f func()) {
	s.timingWheel.AfterFunc(d, f)
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}

// 获取或请求tag
// 先根据tagKey在本地取，如果没有，则去频道领导者节点取
// 取到后设置在本地缓存中
// targetNodeId 目标节点的uids，如果为0，表示获取所有节点的uids
func (s *Service) GetOrRequestAndMakeTag(fakeChannelId string, channelType uint8, tagKey string, targetNodeId uint64) (*types.Tag, error) {

	// realFakeChannelId := fakeChannelId
	// if options.G.IsCmdChannel(fakeChannelId) {
	// 	realFakeChannelId = options.G.CmdChannelConvertOrginalChannel(fakeChannelId)
	// }

	var tag *types.Tag
	if tagKey == "" {
		tagKey = service.TagManager.GetChannelTag(fakeChannelId, channelType)
	}
	if tagKey != "" {
		tag = service.TagManager.Get(tagKey)
	}
	if tag != nil {
		return tag, nil
	}
	if channelType == wkproto.ChannelTypePerson {
		return s.getOrMakePersonTag(fakeChannelId)
	}

	slotLeaderId, err := service.Cluster.SlotLeaderIdOfChannel(fakeChannelId, channelType)
	if err != nil {
		wklog.Error("GetOrRequestTag: getLeaderOfChannel failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}
	if slotLeaderId == 0 {
		wklog.Warn("GetOrRequestTag: slotLeader is 0", zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, errors.ChannelNotExist(fakeChannelId)
	}

	// tagKey在频道的领导节点是一定存在的，
	// 如果不存在可能就是失效了，这里直接忽略,只能等下条消息触发重构tag
	if options.G.IsLocalNode(slotLeaderId) {
		wklog.Warn("tag not exist in leader node", zap.String("tagKey", tagKey), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, errors.TagNotExist(tagKey)
	}

	// 去领导节点请求
	tagResp, err := s.client.RequestTag(slotLeaderId, &ingress.TagReq{
		TagKey:      tagKey,
		ChannelId:   fakeChannelId,
		ChannelType: channelType,
		NodeId:      targetNodeId,
	})
	if err != nil {
		s.Error("GetOrRequestTag: get tag failed", zap.Error(err), zap.Uint64("slotLeaderId", slotLeaderId), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}

	tag, err = service.TagManager.MakeTagWithTagKey(tagKey, tagResp.Uids)
	if err != nil {
		s.Error("GetOrRequestTag: make tag failed", zap.Error(err))
		return nil, err
	}
	service.TagManager.SetChannelTag(fakeChannelId, channelType, tagKey)
	return tag, nil
}

func (s *Service) GetOrRequestAndMakeTagWithLocal(fakeChannelId string, channelType uint8, tagKey string) (*types.Tag, error) {
	return s.GetOrRequestAndMakeTag(fakeChannelId, channelType, tagKey, options.G.Cluster.NodeId)
}

// 获取个人频道的投递tag
func (s *Service) getOrMakePersonTag(fakeChannelId string) (*types.Tag, error) {

	realFakeChannelId := fakeChannelId
	if options.G.IsCmdChannel(fakeChannelId) {
		realFakeChannelId = options.G.CmdChannelConvertOrginalChannel(fakeChannelId)
	}
	// 处理普通假个人频道
	u1, u2 := options.GetFromUIDAndToUIDWith(realFakeChannelId)
	subscribers := []string{u1, u2}
	tag, err := service.TagManager.MakeTag(subscribers)
	if err != nil {
		return nil, err
	}
	service.TagManager.SetChannelTag(fakeChannelId, wkproto.ChannelTypePerson, tag.Key)
	return tag, nil
}

func (s *Service) GetStreamsForLocal(clientMsgNos []string) ([]*wkdb.StreamV2, error) {
	streamResps := make([]*wkdb.StreamV2, 0, len(clientMsgNos)) // 流消息集合
	noCacheClientMsgNos := make([]string, 0, len(clientMsgNos)) // 没有缓存的流消息id
	for _, clientMsgNo := range clientMsgNos {
		stream, err := service.StreamCache.GetStream(clientMsgNo)
		if err != nil && err != wkcache.ErrStreamNotFound {
			s.Warn("GetStreams: get stream failed", zap.Error(err), zap.String("clientMsgNo", clientMsgNo))
			continue
		}
		if stream == nil {
			noCacheClientMsgNos = append(noCacheClientMsgNos, clientMsgNo)
			continue
		}
		meta := stream.Meta
		payload := service.StreamCache.GetStreamData(stream)
		streamResps = append(streamResps, &wkdb.StreamV2{
			ClientMsgNo: clientMsgNo,
			MessageId:   meta.MessageId,
			ChannelId:   meta.ChannelId,
			ChannelType: meta.ChannelType,
			FromUid:     meta.FromUid,
			End:         meta.EndReason,
			EndReason:   meta.EndReason,
			Payload:     payload,
		})
	}

	if len(noCacheClientMsgNos) > 0 {
		streamV2s, err := service.Store.GetStreamV2s(noCacheClientMsgNos)
		if err != nil {
			s.Warn("GetStreams: get stream failed", zap.Error(err))
			return nil, err
		}
		streamResps = append(streamResps, streamV2s...)
	}
	return streamResps, nil
}

// 检查连接的真实性 并获取真实连接
func CheckConnValidAndGetRealConn(conn *eventbus.Conn) (wknet.Conn, error) {
	// 获取到真实连接
	realConn := service.ConnManager.GetConn(conn.ConnId)
	if realConn == nil {
		return nil, nil
	}

	// 验证连接的真实性, 双向验证，验证连接的fd和connId是否一致
	// 1. 通过fd获取连接
	connfd := realConn.Fd().Fd()
	realConnByFd := service.ConnManager.GetConnByFd(connfd)
	if realConnByFd == nil {
		// connId与fd不一致，说明连接已经关闭
		return nil, fmt.Errorf("connId not match, connId: %d, fd: %d", conn.ConnId, connfd)
	}

	ctx, ctxByFd := realConn.Context(), realConnByFd.Context()

	if ctx != ctxByFd {
		// connId与fd不一致，说明连接已经关闭
		return nil, fmt.Errorf("context not match, connId: %d, fd: %d", conn.ConnId, connfd)
	}

	// 2. 验证连接的fd和connId是否一致
	if ctxByFd != nil {
		eventConn, ok := ctxByFd.(*eventbus.Conn)
		if ok && eventConn != nil {
			if eventConn.ConnId != conn.ConnId {
				return nil, fmt.Errorf("connId not match, connId: %d, realConnId: %d", conn.ConnId, eventConn.ConnId)
			}
		}
	}
	return realConn, nil
}
