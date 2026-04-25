package delivery

import runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"

var ErrInvalidPersonChannel = runtimechannelid.ErrInvalidPersonChannel

func EncodePersonChannel(leftUID, rightUID string) string {
	return runtimechannelid.EncodePersonChannel(leftUID, rightUID)
}

func DecodePersonChannel(channelID string) (string, string, error) {
	return runtimechannelid.DecodePersonChannel(channelID)
}

func NormalizePersonChannel(senderUID, channelID string) (string, error) {
	return runtimechannelid.NormalizePersonChannel(senderUID, channelID)
}
