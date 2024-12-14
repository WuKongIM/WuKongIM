package reactor

import wkproto "github.com/WuKongIM/WuKongIMGoProto"

var User *UserPlus
var Channel *ChannelPlus
var Proto wkproto.Protocol = wkproto.New()

func RegisterUser(u IUser) {
	User = newUserPlus(u)
}

func RegisterChannel(c IChannel) {
	Channel = newChannelPlus(c)
}
