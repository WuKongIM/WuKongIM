package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"go.uber.org/zap"
)

func (d *Diffuse) processDiffuse(msg *reactor.ChannelMessage) {
	tag, err := d.commonService.GetOrRequestAndMakeTag(msg.FakeChannelId, msg.ChannelType, msg.TagKey)
	if err != nil {
		fmt.Println("processDiffuse: get tag failed", zap.Error(err), zap.String("fakeChannelId", msg.FakeChannelId), zap.Uint8("channelType", msg.ChannelType), zap.String("tagKey", msg.TagKey))
	}
	if tag == nil {
		d.Error("processDiffuse: tag not found", zap.String("tagKey", msg.TagKey), zap.String("channelId", msg.FakeChannelId), zap.Uint8("channelType", msg.ChannelType))
		return
	}

	// 根据用户所在节点，将消息推送到对应节点
	for _, node := range tag.Nodes {
		if !options.G.IsLocalNode(node.LeaderId) {
			continue
		}
		for _, uid := range node.Uids {
			cloneMsg := msg.Clone()
			cloneMsg.ToUid = uid
			cloneMsg.MsgType = reactor.ChannelMsgPush
			reactor.Push.PushMessage(cloneMsg)
		}
	}
}
