package handler

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

const keyPrefix = "channel/"

func KeyFromChannelID(id channel.ChannelID) channel.ChannelKey {
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(id.ID))
	buf := make([]byte, 0, len(keyPrefix)+4+1+len(encodedID))
	buf = append(buf, keyPrefix...)
	buf = strconv.AppendUint(buf, uint64(id.Type), 10)
	buf = append(buf, '/')
	buf = append(buf, encodedID...)
	return channel.ChannelKey(buf)
}

func ParseChannelKey(key channel.ChannelKey) (channel.ChannelID, error) {
	parts := strings.SplitN(string(key), "/", 3)
	if len(parts) != 3 || parts[0] != strings.TrimSuffix(keyPrefix, "/") {
		return channel.ChannelID{}, channel.ErrInvalidMeta
	}
	channelType, err := strconv.ParseUint(parts[1], 10, 8)
	if err != nil {
		return channel.ChannelID{}, channel.ErrInvalidMeta
	}
	rawID, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return channel.ChannelID{}, channel.ErrInvalidMeta
	}
	return channel.ChannelID{ID: string(rawID), Type: uint8(channelType)}, nil
}
