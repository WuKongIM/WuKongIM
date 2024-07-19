package wkutil

import (
	"strconv"
	"strings"
)

func ChannelToKey(channelId string, channelType uint8) string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(int(channelType)))
	builder.WriteString("&")
	builder.WriteString(channelId)
	return builder.String()

}

func ChannelFromlKey(channelKey string) (string, uint8) {
	channels := strings.Split(channelKey, "&")
	if len(channels) == 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return channels[1], uint8(channelTypeI)
	} else if len(channels) > 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return strings.Join(channels[1:], ""), uint8(channelTypeI)

	}
	return "", 0
}
