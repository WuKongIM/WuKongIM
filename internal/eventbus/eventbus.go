package eventbus

import (
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// RegisterUser 注册用户事件
func RegisterUser(user IUser) {
	User = newUserPlus(user)
}

func RegisterChannel(channel IChannel) {
	Channel = newChannelPlus(channel)
}

var userEventRouteMap = make(map[EventType][]UserHandlerFunc)
var channelEventRouteMap = make(map[EventType][]ChannelHandlerFunc)

// RegisterUserHandlers 注册事件流程
func RegisterUserHandlers(eventType EventType, handlers ...UserHandlerFunc) {
	userEventRouteMap[eventType] = handlers
}

// RegisterChannelHandlers 注册频道事件
func RegisterChannelHandlers(eventType EventType, handlers ...ChannelHandlerFunc) {
	channelEventRouteMap[eventType] = handlers
}

// ExecuteUserEvent 执行用户事件
func ExecuteUserEvent(ctx *UserContext) {
	handlers, ok := userEventRouteMap[ctx.EventType]
	if !ok {
		return
	}
	for _, handler := range handlers {
		handler(ctx)
	}
}

func ExecuteChannelEvent(ctx *ChannelContext) {
	handlers, ok := channelEventRouteMap[ctx.EventType]
	if !ok {
		return
	}
	for _, handler := range handlers {
		handler(ctx)
	}
}

// 通讯协议
var Proto wkproto.Protocol = wkproto.New()
