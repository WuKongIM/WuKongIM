package webhook

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"
)

type messageResp struct {
	Header       messageHeader `json:"header"`
	Setting      uint8         `json:"setting"`
	MessageID    uint64        `json:"message_id"`
	MessageIDStr string        `json:"message_idstr"`
	ClientMsgNo  string        `json:"client_msg_no"`
	MessageSeq   uint64        `json:"message_seq"`
	FromUID      string        `json:"from_uid"`
	ChannelID    string        `json:"channel_id"`
	ChannelType  uint8         `json:"channel_type"`
	Timestamp    int32         `json:"timestamp"`
	Payload      []byte        `json:"payload"`
}

type messageHeader struct {
	NoPersist uint8 `json:"no_persist"`
	RedDot    uint8 `json:"red_dot"`
	SyncOnce  uint8 `json:"sync_once"`
}

type offlineResp struct {
	MessageResp
	ToUIDs         []string `json:"to_uids,omitempty"`
	Compress       string   `json:"compress,omitempty"`
	CompressToUIDs string   `json:"compress_to_uids,omitempty"`
	SourceID       int64    `json:"source_id,omitempty"`
}

// MessageResp exposes the legacy-compatible encoded message shape for app adapters.
type MessageResp = messageResp

func buildNotifyBody(messages []Message) ([]byte, error) {
	out := make([]messageResp, 0, len(messages))
	for _, msg := range messages {
		out = append(out, messageRespFromMessage(msg))
	}
	return json.Marshal(out)
}

func buildOfflineBody(message OfflineMessage, compressThreshold int) ([]byte, error) {
	resp := offlineResp{MessageResp: messageRespFromMessage(message.Message)}
	if compressThreshold > 0 && len(message.ToUIDs) >= compressThreshold {
		compressed, err := gzipJSONStringSlice(message.ToUIDs)
		if err != nil {
			return nil, err
		}
		resp.Compress = "gzip"
		resp.CompressToUIDs = base64.StdEncoding.EncodeToString(compressed)
	} else {
		resp.ToUIDs = append([]string(nil), message.ToUIDs...)
	}
	return json.Marshal(resp)
}

func buildOnlineStatusBody(statuses []OnlineStatus) ([]byte, error) {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Value != "" {
			values = append(values, status.Value)
		}
	}
	return json.Marshal(values)
}

func messageRespFromMessage(msg Message) messageResp {
	return messageResp{
		Header: messageHeader{
			NoPersist: 0,
			RedDot:    boolToUint8(msg.RedDot),
			SyncOnce:  boolToUint8(msg.SyncOnce),
		},
		Setting:      msg.Setting,
		MessageID:    msg.MessageID,
		MessageIDStr: uint64String(msg.MessageID),
		ClientMsgNo:  msg.ClientMsgNo,
		MessageSeq:   msg.MessageSeq,
		FromUID:      msg.FromUID,
		ChannelID:    msg.ChannelID,
		ChannelType:  msg.ChannelType,
		Timestamp:    int32(time.UnixMilli(msg.ServerTimestampMS).Unix()),
		Payload:      append([]byte(nil), msg.Payload...),
	}
}

func gzipJSONStringSlice(values []string) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(values); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func boolToUint8(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}

func uint64String(v uint64) string {
	return strconv.FormatUint(v, 10)
}
