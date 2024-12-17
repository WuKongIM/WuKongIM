package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"go.uber.org/zap"
)

func (d *Diffuse) processDiffuse(msg *reactor.ChannelMessage) {
	tag, err := d.getOrMakeTag(msg.FakeChannelId, msg.ChannelType)
	if err != nil {
		d.Error("diffuse: getAndMakeTag failed", zap.Error(err))
		return
	}

	for _, node := range tag.Nodes {
		if options.G.IsLocalNode(node.LeaderId) {
			for _, uid := range node.Uids {
				cloneMsg := msg.Clone()
				cloneMsg.ToUid = uid
				cloneMsg.MsgType = reactor.ChannelMsgPush
				reactor.Push.PushMessage(cloneMsg)
			}
		} else {
			// 如果不是本节点，则转发给对应节点
			cloneMsg := msg.Clone()
			cloneMsg.TagKey = tag.Key
			cloneMsg.ToNode = node.LeaderId
			reactor.Diffuse.AddMessageToOutbound(node.LeaderId, cloneMsg)
		}
	}
}

func (d *Diffuse) getOrMakeTag(fakeChannelId string, channelType uint8) (*types.Tag, error) {
	var (
		tag *types.Tag
		err error
	)
	tagKey := service.TagMananger.GetChannelTag(fakeChannelId, channelType)
	if tagKey != "" {
		tag = service.TagMananger.Get(tagKey)
	}
	if tag == nil {
		tag, err = d.makeChannelTag(fakeChannelId, channelType)
		if err != nil {
			d.Error("diffuse: makeTag failed", zap.Error(err), zap.String("tagKey", tagKey))
			return nil, err
		}
	}
	return tag, nil
}

func (d *Diffuse) makeChannelTag(fakeChannelId string, channelType uint8) (*types.Tag, error) {
	subscribers, err := service.Store.GetSubscribers(fakeChannelId, channelType)
	if err != nil {
		d.Error("diffuse: getSubscribers failed", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}
	uids := make([]string, 0, len(subscribers))
	for _, sub := range subscribers {
		uids = append(uids, sub.Uid)
	}
	return service.TagMananger.MakeTag(uids)
}
