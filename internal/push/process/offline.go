package process

import (
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
)

// 处理离线消息
func (p *Push) processPushOffline(messages []*reactor.ChannelMessage) {
	service.Webhook.NotifyOfflineMsg(messages)
}
