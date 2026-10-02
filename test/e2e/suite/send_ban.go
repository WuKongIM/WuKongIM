//go:build e2e

package suite

import (
	"context"
	"net/url"
	"strconv"
)

// SendBanPolicy is the public management projection, without credential data.
type SendBanPolicy struct {
	Value   int    `json:"send_ban"`
	Version string `json:"send_ban_version"`
}

// SetUserSendBan changes one UID's policy through Product HTTP.
func SetUserSendBan(ctx context.Context, addr, uid string, value int) (SendBanPolicy, error) {
	return writeSendBanPolicy(ctx, addr, "/user/send_ban", map[string]any{"uid": uid, "send_ban": value})
}

// SetChannelSendBan changes one source Channel's policy through Product HTTP.
func SetChannelSendBan(ctx context.Context, addr, id string, kind uint8, value int) (SendBanPolicy, error) {
	return writeSendBanPolicy(ctx, addr, "/channel/send_ban", map[string]any{"channel_id": id, "channel_type": kind, "send_ban": value})
}

func writeSendBanPolicy(ctx context.Context, addr, path string, body any) (SendBanPolicy, error) {
	var out struct {
		Data SendBanPolicy `json:"data"`
	}
	_, err := PostJSON(ctx, "http://"+addr+path, body, &out)
	return out.Data, err
}

// GetUserSendBan reads a UID's current authoritative policy through Product HTTP.
func GetUserSendBan(ctx context.Context, addr, uid string) (SendBanPolicy, error) {
	return readSendBanPolicy(ctx, addr, "/user/send_ban", url.Values{"uid": {uid}})
}

// GetChannelSendBan reads a source Channel's authoritative policy through Product HTTP.
func GetChannelSendBan(ctx context.Context, addr, id string, kind uint8) (SendBanPolicy, error) {
	return readSendBanPolicy(ctx, addr, "/channel/send_ban", url.Values{"channel_id": {id}, "channel_type": {strconv.Itoa(int(kind))}})
}

func readSendBanPolicy(ctx context.Context, addr, path string, query url.Values) (SendBanPolicy, error) {
	var out struct {
		Data SendBanPolicy `json:"data"`
	}
	_, err := GetJSON(ctx, "http://"+addr+path+"?"+query.Encode(), &out)
	return out.Data, err
}
