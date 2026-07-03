package handler

import (
	"encoding/base64"
	"strconv"
	"strings"
	"unsafe"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

const keyPrefix = "channel/"

// KeyFromChannelID returns the stable runtime key for a logical channel ID.
func KeyFromChannelID(id channel.ChannelID) channel.ChannelKey {
	encodedIDLen := base64.RawURLEncoding.EncodedLen(len(id.ID))
	bufLen := len(keyPrefix) + decimalUint8Len(id.Type) + 1 + encodedIDLen
	buf := make([]byte, bufLen)
	pos := 0
	pos += copy(buf[pos:], keyPrefix)
	pos += appendUint8ToFixed(buf[pos:], id.Type)
	buf[pos] = '/'
	pos++
	base64.RawURLEncoding.Encode(buf[pos:], unsafeStringBytes(id.ID))
	return channel.ChannelKey(unsafe.String(&buf[0], len(buf)))
}

func decimalUint8Len(value uint8) int {
	switch {
	case value >= 100:
		return 3
	case value >= 10:
		return 2
	default:
		return 1
	}
}

func appendUint8ToFixed(dst []byte, value uint8) int {
	if value >= 100 {
		dst[0] = '0' + value/100
		dst[1] = '0' + value/10%10
		dst[2] = '0' + value%10
		return 3
	}
	if value >= 10 {
		dst[0] = '0' + value/10
		dst[1] = '0' + value%10
		return 2
	}
	dst[0] = '0' + value
	return 1
}

// unsafeStringBytes returns a read-only byte view of value without allocating.
func unsafeStringBytes(value string) []byte {
	return unsafe.Slice(unsafe.StringData(value), len(value))
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
