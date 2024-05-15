package server

import (
	"fmt"

	"go.uber.org/zap"
)

func (c *channel) step(a *ChannelAction) error {

	switch a.ActionType {
	case ChannelActionSend: // 发送
		fromUid := a.Messages[0].FromUid // 消息是按照发送者分组的，所以取第一个即可
		if c.hotspotSender(fromUid) {    // 是热点发送者说明有权限发送消息，就不需要做权限校验了
			c.appendMessage(a.Messages...)
		} else { // 不是热点发送者，需要校验权限
			c.exec(&ChannelAction{ActionType: ChannelActionPermission, Messages: a.Messages})
		}

	case ChannelActionPermissionResp: // 权限校验返回
		if len(a.Messages) == 0 {
			return nil
		}
		if a.Reason == ReasonSuccess {
			// fmt.Println("权限校验成功....", len(a.Messages))
			c.appendMessage(a.Messages...)
			fromUid := a.Messages[0].FromUid
			c.setHotspotSender(fromUid)
		} else {
			// 权限校验失败，需要通知发送者
			c.exec(&ChannelAction{ActionType: ChannelActionSendack, Messages: a.Messages, Reason: a.Reason})
		}

	case ChannelActionStorageResp: // 存储完成
		if a.Reason == ReasonSuccess {
			lastMsg := a.Messages[len(a.Messages)-1]
			if lastMsg.Index == c.msgQueue.storagingIndex {
				c.msgQueue.storagedIndex = lastMsg.Index
				// fmt.Println("存储消息成功--->", lastMsg.Index)
			} else {
				c.Error("严重，存储不是按照下表顺序进行的！", zap.Uint64("lastIndex", lastMsg.Index), zap.Uint64("storagingIndex", c.msgQueue.storagingIndex))
			}
		}
		// 消息存储完毕后，需要通知发送者
		c.exec(&ChannelAction{ActionType: ChannelActionSendack, Messages: a.Messages, Reason: a.Reason})

	case ChannelActionDeliverResp: // 消息投递返回
		if len(a.Messages) == 0 {
			fmt.Println("messages is empty")
			return nil
		}
		lastMsg := a.Messages[len(a.Messages)-1]
		// fmt.Println("投递消息成功--->", lastMsg.Index)
		c.msgQueue.deliverTo(lastMsg.Index)
	}

	return nil
}

func (c *channel) exec(a *ChannelAction) {
	c.actions = append(c.actions, a)
}

func (c *channel) appendMessage(messages ...*ReactorChannelMessage) {

	for _, message := range messages {
		message.Index = c.msgQueue.lastIndex + 1
		c.msgQueue.appendMessage(message)
	}

}
