package reactor

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

// 用户行为
var User *UserPlus

// 频道行为
var Channel *ChannelPlus

// 消息扩散
var Diffuse *DiffusePlus

// 推送
var Push *PushPlus

// 通讯协议
var Proto wkproto.Protocol = wkproto.New()

func RegisterUser(u IUser) {
	User = newUserPlus(u)
}

func RegisterChannel(c IChannel) {
	Channel = newChannelPlus(c)
}

func RegisterDiffuse(d IDiffuse) {
	Diffuse = newDiffusePlus(d)
}

func RegisterPush(p IPush) {
	Push = newPushPlus(p)
}
