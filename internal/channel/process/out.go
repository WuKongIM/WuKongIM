package process

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (c *Channel) Send(actions []reactor.ChannelAction) {

	var err error
	for _, a := range actions {
		err = c.processPool.Submit(func() {
			c.processAction(a)
		})
		if err != nil {
			c.Error("submit err", zap.Error(err), zap.String("channelId", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType), zap.String("actionType", a.Type.String()))
			continue
		}
	}
}

// 频道行为逻辑处理
func (c *Channel) processAction(a reactor.ChannelAction) {
	fmt.Println("action------>", a.Type.String())
	switch a.Type {
	case reactor.ChannelActionElection: // 选举
		c.processElection(a)
	case reactor.ChannelActionInbound: // 处理收件箱
		c.processInbound(a)
	}
}

func (c *Channel) processElection(a reactor.ChannelAction) {
	channelKey := wkutil.ChannelToKey(a.FakeChannelId, a.ChannelType)
	if c.isElectioning(channelKey) {
		return
	}
	defer c.unsetElectioning(channelKey)

	c.setElectioning(channelKey)

	timeoutCtx, cancel := c.WithTimeout()
	defer cancel()

	cfg, err := service.Cluster.LoadOrCreateChannel(timeoutCtx, a.FakeChannelId, a.ChannelType)
	if err != nil {
		c.Error("load or create channel failed", zap.Error(err), zap.String("channelId", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType))
		return
	}
	if cfg.LeaderId == 0 {
		c.Error("election leader failed", zap.String("channelId", a.FakeChannelId), zap.Uint8("channelType", a.ChannelType))
		return
	}
	reactor.Channel.UpdateConfig(a.FakeChannelId, a.ChannelType, reactor.ChannelConfig{
		LeaderId: cfg.LeaderId,
	})
}

func (c *Channel) processInbound(a reactor.ChannelAction) {
	if len(a.Messages) == 0 {
		return
	}
	var (
		storageMessages            []*reactor.ChannelMessage // 存储的消息
		storageNotifyQueueMessages []*reactor.ChannelMessage // 存储通知队列的消息
		sendackMessages            []*reactor.ChannelMessage // 发送回执的消息
	)
	for _, m := range a.Messages {
		switch m.MsgType {
		// 消息发送
		case reactor.ChannelMsgSend:
			c.processSend(a.Role, m)
			// 权限验证
		case reactor.ChannelMsgPermission:
			c.processPermission(a.ChannelInfo, m)
			// 消息存储
		case reactor.ChannelMsgStorage:
			if storageMessages == nil {
				storageMessages = make([]*reactor.ChannelMessage, 0, len(a.Messages))
			}
			storageMessages = append(storageMessages, m)
		case reactor.ChannelMsgStorageNotifyQueue:
			if storageNotifyQueueMessages == nil {
				storageNotifyQueueMessages = make([]*reactor.ChannelMessage, 0, len(a.Messages))
			}
			storageNotifyQueueMessages = append(storageNotifyQueueMessages, m)
			// 消息发送回执
		case reactor.ChannelMsgSendack:
			if sendackMessages == nil {
				sendackMessages = make([]*reactor.ChannelMessage, 0, len(a.Messages))
			}
			sendackMessages = append(sendackMessages, m)
		}
	}
	if len(storageMessages) > 0 {
		c.processStorage(a.FakeChannelId, a.ChannelType, storageMessages)
	}
	if len(storageNotifyQueueMessages) > 0 {
		c.processStorageNotifyQueue(a.FakeChannelId, a.ChannelType, storageNotifyQueueMessages)
	}
	if len(sendackMessages) > 0 {
		c.processSendack(sendackMessages)
	}
}
