package common

import (
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/errors"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
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

	leader, err := service.Cluster.LeaderOfChannel(fakeChannelId, channelType)
	if err != nil {
		wklog.Error("GetOrRequestTag: getLeaderOfChannel failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}
	if leader == nil {
		wklog.Warn("GetOrRequestTag: leader is nil", zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, errors.ChannelNotExist(fakeChannelId)
	}

	// tagKey在频道的领导节点是一定存在的，
	// 如果不存在可能就是失效了，这里直接忽略,只能等下条消息触发重构tag
	if options.G.IsLocalNode(leader.Id) {
		wklog.Warn("tag not exist in leader node", zap.String("tagKey", tagKey), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, errors.TagNotExist(tagKey)
	}

	// 去领导节点请求
	tagResp, err := s.client.RequestTag(leader.Id, &ingress.TagReq{
		TagKey:      tagKey,
		ChannelId:   fakeChannelId,
		ChannelType: channelType,
		NodeId:      targetNodeId,
	})
	if err != nil {
		s.Error("GetOrRequestTag: get tag failed", zap.Error(err), zap.Uint64("leaderId", leader.Id), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
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
