package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/service"
)

func (h *Handler) conversation(channelId string, channelType uint8, tagKey string, events []*eventbus.Event) {
	service.ConversationManager.Push(channelId, channelType, tagKey, events)
}
