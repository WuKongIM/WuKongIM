package jsonrpc

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Constants based on the schema enums

type DeviceFlagEnum int

const (
	DeviceApp DeviceFlagEnum = 0
	DeviceWeb DeviceFlagEnum = 1
	DeviceSys DeviceFlagEnum = 2
)

type ReasonCodeEnum int

// Add specific ReasonCode values if available in frame.ReasonCode
// Example:
// const (
//    ReasonCodeSuccess ReasonCodeEnum = 0
//    ReasonCodeAuthFailed ReasonCodeEnum = 1
//    // ... other reason codes
// )

type StreamFlagEnum int

const (
	StreamStart StreamFlagEnum = 0
	StreamIng   StreamFlagEnum = 1
	StreamEnd   StreamFlagEnum = 2
)

type ActionEnum int

const (
	ActionSubscribe   ActionEnum = 0
	ActionUnsubscribe ActionEnum = 1
)

// Shared structures

type Header struct {
	NoPersist bool `json:"noPersist,omitempty"`
	RedDot    bool `json:"redDot,omitempty"`
	SyncOnce  bool `json:"syncOnce,omitempty"`
	Dup       bool `json:"dup,omitempty"`
	End       bool `json:"end,omitempty"`
}

// UnmarshalJSON accepts both naming conventions used by EasySDK headers.
func (h *Header) UnmarshalJSON(data []byte) error {
	var wire struct {
		NoPersist      *bool `json:"noPersist"`
		NoPersistSnake *bool `json:"no_persist"`
		RedDot         *bool `json:"redDot"`
		RedDotSnake    *bool `json:"red_dot"`
		SyncOnce       *bool `json:"syncOnce"`
		SyncOnceSnake  *bool `json:"sync_once"`
		Dup            *bool `json:"dup"`
		DupUpper       *bool `json:"DUP"`
		End            *bool `json:"end"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*h = Header{}
	if wire.NoPersist != nil {
		h.NoPersist = *wire.NoPersist
	} else if wire.NoPersistSnake != nil {
		h.NoPersist = *wire.NoPersistSnake
	}
	if wire.RedDot != nil {
		h.RedDot = *wire.RedDot
	} else if wire.RedDotSnake != nil {
		h.RedDot = *wire.RedDotSnake
	}
	if wire.SyncOnce != nil {
		h.SyncOnce = *wire.SyncOnce
	} else if wire.SyncOnceSnake != nil {
		h.SyncOnce = *wire.SyncOnceSnake
	}
	if wire.Dup != nil {
		h.Dup = *wire.Dup
	} else if wire.DupUpper != nil {
		h.Dup = *wire.DupUpper
	}
	if wire.End != nil {
		h.End = *wire.End
	}
	return nil
}

type SettingFlags struct {
	Receipt bool `json:"receipt,omitempty"`
	Signal  bool `json:"signal,omitempty"`
	Stream  bool `json:"stream,omitempty"`
	Topic   bool `json:"topic,omitempty"`
}

type ErrorObject struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"` // Keep generic for flexibility
}

// Base request/response structure components

type BaseRequest struct {
	Jsonrpc string `json:"jsonrpc,omitempty"`
	Method  string `json:"method"`
	ID      string `json:"id,omitempty"`
}

type BaseResponse struct {
	Jsonrpc string       `json:"jsonrpc,omitempty"`
	ID      string       `json:"id,omitempty"`
	Error   *ErrorObject `json:"error,omitempty"`
}

type BaseNotification struct {
	Jsonrpc string `json:"jsonrpc,omitempty"`
	Method  string `json:"method"`
}

// --- Specific Request Payloads (Params) ---

type ConnectParams struct {
	Header          Header         `json:"header,omitempty"`
	Version         int            `json:"version,omitempty"`
	ClientKey       string         `json:"clientKey,omitempty"`
	DeviceID        string         `json:"deviceId,omitempty"`
	DeviceFlag      DeviceFlagEnum `json:"deviceFlag"`
	ClientTimestamp int64          `json:"clientTimestamp,omitempty"`
	UID             string         `json:"uid"`
	Token           string         `json:"token"`
}

// UnmarshalJSON accepts the field names emitted by the released EasySDKs.
// Android uses snake_case while iOS, Flutter, and JavaScript use camelCase.
func (p *ConnectParams) UnmarshalJSON(data []byte) error {
	var wire struct {
		Header               Header          `json:"header"`
		Version              int             `json:"version"`
		ClientKey            *string         `json:"clientKey"`
		ClientKeySnake       *string         `json:"client_key"`
		DeviceID             *string         `json:"deviceId"`
		DeviceIDSnake        *string         `json:"device_id"`
		DeviceFlag           *DeviceFlagEnum `json:"deviceFlag"`
		DeviceFlagSnake      *DeviceFlagEnum `json:"device_flag"`
		ClientTimestamp      *int64          `json:"clientTimestamp"`
		ClientTimestampSnake *int64          `json:"client_timestamp"`
		UID                  string          `json:"uid"`
		Token                string          `json:"token"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*p = ConnectParams{
		Header:  wire.Header,
		Version: wire.Version,
		UID:     wire.UID,
		Token:   wire.Token,
	}
	if wire.ClientKey != nil {
		p.ClientKey = *wire.ClientKey
	} else if wire.ClientKeySnake != nil {
		p.ClientKey = *wire.ClientKeySnake
	}
	if wire.DeviceID != nil {
		p.DeviceID = *wire.DeviceID
	} else if wire.DeviceIDSnake != nil {
		p.DeviceID = *wire.DeviceIDSnake
	}
	if wire.DeviceFlag != nil {
		p.DeviceFlag = *wire.DeviceFlag
	} else if wire.DeviceFlagSnake != nil {
		p.DeviceFlag = *wire.DeviceFlagSnake
	}
	if wire.ClientTimestamp != nil {
		p.ClientTimestamp = *wire.ClientTimestamp
	} else if wire.ClientTimestampSnake != nil {
		p.ClientTimestamp = *wire.ClientTimestampSnake
	}
	return nil
}

type SendParams struct {
	Header      Header       `json:"header,omitempty"`
	Setting     SettingFlags `json:"setting,omitempty"`
	MsgKey      string       `json:"msgKey,omitempty"`
	Expire      uint32       `json:"expire,omitempty"`
	ClientMsgNo string       `json:"clientMsgNo,omitempty"`
	StreamNo    string       `json:"streamNo,omitempty"`
	ChannelID   string       `json:"channelId"`
	ChannelType int          `json:"channelType"`
	Topic       string       `json:"topic,omitempty"`
	Payload     []byte       `json:"payload"`
}

// UnmarshalJSON accepts the payload and field-name variants emitted by the
// released EasySDK clients. Android uses snake_case with JSON encoded as a
// string, while the other clients use camelCase with objects or Base64.
func (p *SendParams) UnmarshalJSON(data []byte) error {
	var wire struct {
		Header           Header          `json:"header"`
		Setting          SettingFlags    `json:"setting"`
		MsgKey           *string         `json:"msgKey"`
		MsgKeySnake      *string         `json:"msg_key"`
		Expire           uint32          `json:"expire"`
		ClientMsgNo      *string         `json:"clientMsgNo"`
		ClientMsgNoSnake *string         `json:"client_msg_no"`
		StreamNo         *string         `json:"streamNo"`
		StreamNoSnake    *string         `json:"stream_no"`
		ChannelID        *string         `json:"channelId"`
		ChannelIDSnake   *string         `json:"channel_id"`
		ChannelType      *int            `json:"channelType"`
		ChannelTypeSnake *int            `json:"channel_type"`
		Topic            string          `json:"topic"`
		Payload          json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	payload, err := decodeEasySDKPayload(wire.Payload)
	if err != nil {
		return err
	}

	*p = SendParams{
		Header:  wire.Header,
		Setting: wire.Setting,
		Expire:  wire.Expire,
		Topic:   wire.Topic,
		Payload: payload,
	}
	if wire.MsgKey != nil {
		p.MsgKey = *wire.MsgKey
	} else if wire.MsgKeySnake != nil {
		p.MsgKey = *wire.MsgKeySnake
	}
	if wire.ClientMsgNo != nil {
		p.ClientMsgNo = *wire.ClientMsgNo
	} else if wire.ClientMsgNoSnake != nil {
		p.ClientMsgNo = *wire.ClientMsgNoSnake
	}
	if wire.StreamNo != nil {
		p.StreamNo = *wire.StreamNo
	} else if wire.StreamNoSnake != nil {
		p.StreamNo = *wire.StreamNoSnake
	}
	if wire.ChannelID != nil {
		p.ChannelID = *wire.ChannelID
	} else if wire.ChannelIDSnake != nil {
		p.ChannelID = *wire.ChannelIDSnake
	}
	if wire.ChannelType != nil {
		p.ChannelType = *wire.ChannelType
	} else if wire.ChannelTypeSnake != nil {
		p.ChannelType = *wire.ChannelTypeSnake
	}
	return nil
}

func decodeEasySDKPayload(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}
		// Android v1.0.3 serializes its already-encoded JSON message content as
		// a JSON string. Prefer that source-aligned representation before the
		// Base64 form used by Flutter and JavaScript.
		if json.Valid([]byte(text)) {
			return []byte(text), nil
		}
		payload, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 payload: %w", err)
		}
		return payload, nil
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("invalid JSON payload")
	}
	return append([]byte(nil), trimmed...), nil
}

type RecvAckParams struct {
	Header     Header `json:"header,omitempty"`
	MessageID  string `json:"messageId"`
	MessageSeq uint64 `json:"messageSeq"`
}

// UnmarshalJSON accepts Android's snake_case acknowledgment fields and the
// camelCase notification shape used by the other EasySDKs. A missing sequence
// remains zero, matching the frame protocol's optional-sequence behavior.
func (p *RecvAckParams) UnmarshalJSON(data []byte) error {
	var wire struct {
		Header          Header  `json:"header"`
		MessageID       *string `json:"messageId"`
		MessageIDSnake  *string `json:"message_id"`
		MessageSeq      *uint64 `json:"messageSeq"`
		MessageSeqSnake *uint64 `json:"message_seq"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}

	*p = RecvAckParams{Header: wire.Header}
	if wire.MessageID != nil {
		p.MessageID = *wire.MessageID
	} else if wire.MessageIDSnake != nil {
		p.MessageID = *wire.MessageIDSnake
	}
	if wire.MessageSeq != nil {
		p.MessageSeq = *wire.MessageSeq
	} else if wire.MessageSeqSnake != nil {
		p.MessageSeq = *wire.MessageSeqSnake
	}
	return nil
}

type SubscribeParams struct {
	SubNo       string `json:"subNo"`
	ChannelID   string `json:"channelId"`
	ChannelType int    `json:"channelType"`
	Param       string `json:"param,omitempty"`
}

type UnsubscribeParams struct {
	SubNo       string `json:"subNo"`
	ChannelID   string `json:"channelId"`
	ChannelType int    `json:"channelType"`
}

type PingParams struct {
	// Empty struct
}

type DisconnectParams struct {
	ReasonCode ReasonCodeEnum `json:"reasonCode"`
	Reason     string         `json:"reason,omitempty"`
}

// --- Specific Result Payloads ---

type ConnectResult struct {
	Header        *Header        `json:"header,omitempty"`
	ServerVersion int            `json:"serverVersion,omitempty"`
	ServerKey     string         `json:"serverKey,omitempty"`
	Salt          string         `json:"salt,omitempty"`
	TimeDiff      int64          `json:"timeDiff,omitempty"`
	ReasonCode    ReasonCodeEnum `json:"reasonCode"`
	NodeID        uint64         `json:"nodeId"`
}

// MarshalJSON exposes both EasySDK response dialects. Android v1.0.3 reads
// snake_case result fields while the other released clients read camelCase.
func (r ConnectResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Header             *Header        `json:"header,omitempty"`
		ServerVersion      int            `json:"serverVersion"`
		ServerVersionSnake int            `json:"server_version"`
		ServerKey          string         `json:"serverKey"`
		ServerKeySnake     string         `json:"server_key"`
		Salt               string         `json:"salt"`
		TimeDiff           int64          `json:"timeDiff"`
		TimeDiffSnake      int64          `json:"time_diff"`
		ReasonCode         ReasonCodeEnum `json:"reasonCode"`
		ReasonCodeSnake    ReasonCodeEnum `json:"reason_code"`
		NodeID             uint64         `json:"nodeId"`
		NodeIDSnake        uint64         `json:"node_id"`
	}{
		Header:             r.Header,
		ServerVersion:      r.ServerVersion,
		ServerVersionSnake: r.ServerVersion,
		ServerKey:          r.ServerKey,
		ServerKeySnake:     r.ServerKey,
		Salt:               r.Salt,
		TimeDiff:           r.TimeDiff,
		TimeDiffSnake:      r.TimeDiff,
		ReasonCode:         r.ReasonCode,
		ReasonCodeSnake:    r.ReasonCode,
		NodeID:             r.NodeID,
		NodeIDSnake:        r.NodeID,
	})
}

type SendResult struct {
	Header     *Header        `json:"header,omitempty"`
	MessageID  string         `json:"messageId"`
	MessageSeq uint64         `json:"messageSeq"`
	ReasonCode ReasonCodeEnum `json:"reasonCode"`
}

// MarshalJSON exposes both result naming conventions used by EasySDKs.
func (r SendResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Header          *Header        `json:"header,omitempty"`
		MessageID       string         `json:"messageId"`
		MessageIDSnake  string         `json:"message_id"`
		MessageSeq      uint64         `json:"messageSeq"`
		MessageSeqSnake uint64         `json:"message_seq"`
		ReasonCode      ReasonCodeEnum `json:"reasonCode"`
		ReasonCodeSnake ReasonCodeEnum `json:"reason_code"`
	}{
		Header:          r.Header,
		MessageID:       r.MessageID,
		MessageIDSnake:  r.MessageID,
		MessageSeq:      r.MessageSeq,
		MessageSeqSnake: r.MessageSeq,
		ReasonCode:      r.ReasonCode,
		ReasonCodeSnake: r.ReasonCode,
	})
}

type SubscriptionResult struct {
	Header      *Header        `json:"header,omitempty"`
	SubNo       string         `json:"subNo"`
	ChannelID   string         `json:"channelId"`
	ChannelType int            `json:"channelType"`
	Action      ActionEnum     `json:"action"`
	ReasonCode  ReasonCodeEnum `json:"reasonCode"`
}

// Pong result is null according to schema, handled by BaseResponse structure

// --- Specific Notification Payloads (Params) ---

type RecvNotificationParams struct {
	Header      *Header        `json:"header,omitempty"`
	Setting     *SettingFlags  `json:"setting,omitempty"`
	MsgKey      string         `json:"msgKey,omitempty"`
	Expire      uint32         `json:"expire,omitempty"`
	MessageID   string         `json:"messageId"`
	MessageSeq  uint64         `json:"messageSeq"`
	ClientMsgNo string         `json:"clientMsgNo,omitempty"`
	StreamNo    string         `json:"streamNo,omitempty"`
	StreamID    string         `json:"streamId,omitempty"`
	StreamFlag  StreamFlagEnum `json:"streamFlag,omitempty"`
	Timestamp   int32          `json:"timestamp"`
	ChannelID   string         `json:"channelId"`
	ChannelType int            `json:"channelType"`
	Topic       string         `json:"topic,omitempty"`
	FromUID     string         `json:"fromUid"`
	Payload     []byte         `json:"payload"`
}

// UnmarshalJSON mirrors the flexible payload handling used for SEND so receive
// notifications remain round-trippable through the public codec.
func (p *RecvNotificationParams) UnmarshalJSON(data []byte) error {
	type plain RecvNotificationParams
	*p = RecvNotificationParams{}
	wire := struct {
		*plain
		Payload json.RawMessage `json:"payload"`
	}{plain: (*plain)(p)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	payload, err := decodeEasySDKPayload(wire.Payload)
	if err != nil {
		return err
	}
	p.Payload = payload
	return nil
}

// MarshalJSON emits the receive shape all released EasySDK clients can parse:
// header is always an object, and JSON object payload bytes stay JSON objects.
func (p RecvNotificationParams) MarshalJSON() ([]byte, error) {
	header := Header{}
	if p.Header != nil {
		header = *p.Header
	}
	payload := any(base64.StdEncoding.EncodeToString(p.Payload))
	trimmed := bytes.TrimSpace(p.Payload)
	if len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed) {
		payload = json.RawMessage(trimmed)
	}

	return json.Marshal(struct {
		Header      Header         `json:"header"`
		Setting     *SettingFlags  `json:"setting,omitempty"`
		MsgKey      string         `json:"msgKey,omitempty"`
		Expire      uint32         `json:"expire,omitempty"`
		MessageID   string         `json:"messageId"`
		MessageSeq  uint64         `json:"messageSeq"`
		ClientMsgNo string         `json:"clientMsgNo,omitempty"`
		StreamNo    string         `json:"streamNo,omitempty"`
		StreamID    string         `json:"streamId,omitempty"`
		StreamFlag  StreamFlagEnum `json:"streamFlag,omitempty"`
		Timestamp   int32          `json:"timestamp"`
		ChannelID   string         `json:"channelId"`
		ChannelType int            `json:"channelType"`
		Topic       string         `json:"topic,omitempty"`
		FromUID     string         `json:"fromUid"`
		Payload     any            `json:"payload"`
	}{
		Header:      header,
		Setting:     p.Setting,
		MsgKey:      p.MsgKey,
		Expire:      p.Expire,
		MessageID:   p.MessageID,
		MessageSeq:  p.MessageSeq,
		ClientMsgNo: p.ClientMsgNo,
		StreamNo:    p.StreamNo,
		StreamID:    p.StreamID,
		StreamFlag:  p.StreamFlag,
		Timestamp:   p.Timestamp,
		ChannelID:   p.ChannelID,
		ChannelType: p.ChannelType,
		Topic:       p.Topic,
		FromUID:     p.FromUID,
		Payload:     payload,
	})
}

// DisconnectNotificationParams are same as DisconnectParams
type DisconnectNotificationParams DisconnectParams

// EventNotificationParams represents the parameters for event notifications
type EventNotificationParams struct {
	Header    *Header `json:"header,omitempty"`
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Timestamp int64   `json:"timestamp"`
	Data      string  `json:"data"`
}

// --- Full Request/Response/Notification Structures ---
// These combine the base and the specific params/result for easier encoding.

// Requests
type ConnectRequest struct {
	BaseRequest
	Params ConnectParams `json:"params"`
}

type SendRequest struct {
	BaseRequest
	Params SendParams `json:"params"`
}

type RecvAckNotification struct {
	BaseNotification
	Params RecvAckParams `json:"params"`
}

type SubscribeRequest struct {
	BaseRequest
	Params SubscribeParams `json:"params"`
}

type UnsubscribeRequest struct {
	BaseRequest
	Params UnsubscribeParams `json:"params"`
}

type PingRequest struct {
	BaseRequest
	// Use pointer for Params to allow omitting the field entirely when nil
	Params *PingParams `json:"params,omitempty"`
}

type DisconnectRequest struct {
	BaseRequest
	Params DisconnectParams `json:"params"`
}

// Responses
type ConnectResponse struct {
	BaseResponse
	Result *ConnectResult `json:"result,omitempty"`
	Error  *ErrorObject   `json:"error,omitempty"`
}

type SendResponse struct {
	BaseResponse
	Result *SendResult  `json:"result,omitempty"`
	Error  *ErrorObject `json:"error,omitempty"`
}

type SubscriptionResponse struct {
	BaseResponse
	Result *SubscriptionResult `json:"result,omitempty"`
	Error  *ErrorObject        `json:"error,omitempty"`
}

type PongResponse struct {
	BaseResponse
	Result json.RawMessage `json:"result"`
}

type RecvAckResponse struct {
	BaseResponse
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ErrorObject    `json:"error,omitempty"`
}

// Disconnect Request does not seem to have a defined Response structure either.
// Assuming BaseResponse is sufficient.

// Notifications
type RecvNotification struct {
	BaseNotification
	Params RecvNotificationParams `json:"params"`
}

type DisconnectNotification struct {
	BaseNotification
	Params DisconnectNotificationParams `json:"params"`
}

type EventNotification struct {
	BaseNotification
	Params EventNotificationParams `json:"params"`
}

// --- Conversion Methods ---

// toProtoInternal converts JSON-RPC Header to frame.Framer (internal helper)
func (h Header) toProtoInternal() *frame.Framer {
	protoHeader := &frame.Framer{}
	// Assuming direct mapping for boolean flags.
	protoHeader.NoPersist = h.NoPersist
	protoHeader.RedDot = h.RedDot
	protoHeader.SyncOnce = h.SyncOnce
	protoHeader.DUP = h.Dup
	protoHeader.End = h.End
	return protoHeader
}

// ToProto converts JSON-RPC SettingFlags to frame.Setting
func (sf SettingFlags) ToProto() frame.Setting {
	var setting frame.Setting = 0
	if sf.Receipt {
		setting |= frame.SettingReceiptEnabled
	}
	if sf.Signal {
		setting |= frame.SettingSignal
	}
	if sf.Stream {
		setting |= frame.SettingStream
	}
	if sf.Topic {
		setting |= frame.SettingTopic
	}
	return setting
}

// ToProto converts the Header value to its proto representation.
func (h Header) ToProto() *frame.Framer {
	return h.toProtoInternal()
}

// --- Specific Payload Conversions ---

// ToProto converts JSON-RPC ConnectParams to frame.ConnectPacket.
func (p ConnectParams) ToProto() *frame.ConnectPacket {

	var version uint8 = uint8(p.Version)
	if p.Version == 0 {
		version = frame.LatestVersion
	}

	req := &frame.ConnectPacket{
		Framer:          headerToFramer(p.Header),
		Version:         version,
		ClientKey:       p.ClientKey,
		DeviceID:        p.DeviceID,
		DeviceFlag:      frame.DeviceFlag(p.DeviceFlag),
		ClientTimestamp: p.ClientTimestamp,
		UID:             p.UID,
		Token:           p.Token,
	}
	return req
}

// FromProtoConnectAck converts frame.ConnackPacket to JSON-RPC ConnectResult
func FromProtoConnectAck(ack *frame.ConnackPacket) *ConnectResult {
	if ack == nil {
		return nil
	}
	res := &ConnectResult{
		Header:        fromProtoHeader(ack.Framer),
		ServerVersion: int(ack.ServerVersion),
		ServerKey:     ack.ServerKey,
		Salt:          ack.Salt,
		TimeDiff:      ack.TimeDiff,
		ReasonCode:    ReasonCodeEnum(ack.ReasonCode),
		NodeID:        ack.NodeId,
	}
	return res
}

// ToProto converts JSON-RPC SendParams to frame.SendPacket.
func (p SendParams) ToProto() *frame.SendPacket {
	req := &frame.SendPacket{
		Framer:      headerToFramer(p.Header),
		Setting:     p.Setting.ToProto(),
		ClientMsgNo: p.ClientMsgNo,
		ChannelID:   p.ChannelID,
		ChannelType: uint8(p.ChannelType),
		Payload:     p.Payload,
		MsgKey:      p.MsgKey,
		Expire:      p.Expire,
		StreamNo:    p.StreamNo,
		Topic:       p.Topic,
	}
	return req
}

// FromProtoSendAck converts frame.SendackPacket to JSON-RPC SendResult
func FromProtoSendAck(ack *frame.SendackPacket) *SendResult {
	if ack == nil {
		return nil
	}
	messageID := strconv.FormatInt(ack.MessageID, 10)
	res := &SendResult{
		Header:     fromProtoHeader(ack.Framer),
		MessageID:  messageID,
		MessageSeq: ack.MessageSeq,
		ReasonCode: ReasonCodeEnum(ack.ReasonCode),
	}
	return res
}

// ToProto converts JSON-RPC RecvAckParams to frame.RecvackPacket.
func (p RecvAckParams) ToProto() *frame.RecvackPacket {
	msgID, _ := strconv.ParseInt(p.MessageID, 10, 64)
	req := &frame.RecvackPacket{
		Framer:     headerToFramer(p.Header),
		MessageID:  msgID,
		MessageSeq: p.MessageSeq,
	}
	return req
}

// FromProtoRecvPacket converts frame.RecvPacket to JSON-RPC RecvNotificationParams
func FromProtoRecvPacket(pkt *frame.RecvPacket) RecvNotificationParams {

	params := RecvNotificationParams{
		Header:      fromProtoHeader(pkt.Framer),
		Setting:     fromProtoSetting(pkt.Setting),
		MsgKey:      pkt.MsgKey,
		Expire:      pkt.Expire,
		MessageID:   strconv.FormatInt(pkt.MessageID, 10),
		MessageSeq:  pkt.MessageSeq,
		ClientMsgNo: pkt.ClientMsgNo,
		StreamNo:    pkt.StreamNo,
		StreamID:    strconv.FormatUint(pkt.StreamId, 10),
		StreamFlag:  StreamFlagEnum(pkt.StreamFlag),
		Timestamp:   pkt.Timestamp,
		ChannelID:   pkt.ChannelID,
		ChannelType: int(pkt.ChannelType),
		Topic:       pkt.Topic,
		FromUID:     pkt.FromUID,
		Payload:     pkt.Payload,
	}
	return params
}

// ToProto converts JSON-RPC SubscribeParams to frame.SubPacket.
func (p SubscribeParams) ToProto() *frame.SubPacket {
	req := &frame.SubPacket{
		SubNo:       p.SubNo,
		ChannelID:   p.ChannelID,
		ChannelType: uint8(p.ChannelType),
		Action:      frame.Subscribe,
		Param:       p.Param,
	}
	return req
}

// ToProto converts JSON-RPC UnsubscribeParams to an unsubscribe frame.
func (p UnsubscribeParams) ToProto() *frame.SubPacket {
	return &frame.SubPacket{
		SubNo:       p.SubNo,
		ChannelID:   p.ChannelID,
		ChannelType: uint8(p.ChannelType),
		Action:      frame.UnSubscribe,
	}
}

// ToProto converts JSON-RPC DisconnectParams to frame.DisconnectPacket.
func (p DisconnectParams) ToProto() *frame.DisconnectPacket {
	pkt := &frame.DisconnectPacket{
		ReasonCode: frame.ReasonCode(p.ReasonCode),
		Reason:     p.Reason,
	}
	return pkt
}

// FromProtoDisconnectPacket converts frame.DisconnectPacket to JSON-RPC DisconnectNotificationParams
func FromProtoDisconnectPacket(pkt *frame.DisconnectPacket) DisconnectNotificationParams {
	if pkt == nil {
		return DisconnectNotificationParams{}
	}
	params := DisconnectNotificationParams{
		ReasonCode: ReasonCodeEnum(pkt.ReasonCode),
		Reason:     pkt.Reason,
	}
	return params
}

// ToProto converts PingParams to frame.PingPacket.
func (p PingParams) ToProto() *frame.PingPacket {
	return &frame.PingPacket{}
}

// FromProtoPongPacket converts frame.PongPacket to PongResponse fields (mostly base)
// Pong response usually just confirms the ID, result is often null.
func FromProtoPongPacket(pkt *frame.PongPacket) {
	if pkt == nil {
		// return appropriate representation of error or empty/null result
	}
	// Pong has no specific result fields typically.
	// The BaseResponse handles ID and potential errors.
	// Result field in PongResponse is json.RawMessage, likely set to `null`.
}

// --- Reverse Helper Functions (Proto -> JSON-RPC) ---

// fromProtoHeader converts frame.Framer to JSON-RPC Header
func fromProtoHeader(protoHeader frame.Framer) *Header {
	if !protoHeader.NoPersist && !protoHeader.RedDot && !protoHeader.SyncOnce && !protoHeader.DUP && !protoHeader.End {
		return nil
	}
	return &Header{
		NoPersist: protoHeader.NoPersist,
		RedDot:    protoHeader.RedDot,
		SyncOnce:  protoHeader.SyncOnce,
		Dup:       protoHeader.DUP,
		End:       protoHeader.End,
	}
}

func headerToFramer(header Header) frame.Framer {
	return frame.Framer{
		NoPersist: header.NoPersist,
		RedDot:    header.RedDot,
		SyncOnce:  header.SyncOnce,
		DUP:       header.Dup,
		End:       header.End,
	}
}

// fromProtoSetting converts frame.Setting to JSON-RPC SettingFlags
func fromProtoSetting(setting frame.Setting) *SettingFlags {

	if setting == 0 {
		return nil
	}

	flags := &SettingFlags{}
	flags.Receipt = (setting & frame.SettingReceiptEnabled) != 0
	flags.Signal = (setting & frame.SettingSignal) != 0
	flags.Stream = (setting & frame.SettingStream) != 0
	flags.Topic = (setting & frame.SettingTopic) != 0
	return flags
}

// --- Helper function to create standard requests easily ---
// Might need adjustments if wkframe types are used directly or interfaces change
func NewRequest(method string, id string, params interface{}) interface{} {
	req := BaseRequest{
		Jsonrpc: "2.0",
		Method:  method,
		ID:      id,
	}
	switch p := params.(type) {
	case ConnectParams:
		return ConnectRequest{BaseRequest: req, Params: p}
	case SendParams:
		return SendRequest{BaseRequest: req, Params: p}
	case SubscribeParams:
		return SubscribeRequest{BaseRequest: req, Params: p}
	case UnsubscribeParams:
		return UnsubscribeRequest{BaseRequest: req, Params: p}
	case DisconnectParams:
		return DisconnectRequest{BaseRequest: req, Params: p}
	case PingParams:
		// If PingParams (value) is passed, wrap it in a pointer for PingRequest
		pVal := params.(PingParams)
		return PingRequest{BaseRequest: req, Params: &pVal}
	case *PingParams:
		// If *PingParams (pointer) is passed, use it directly
		return PingRequest{BaseRequest: req, Params: p}
	case nil:
		// If nil is passed specifically for ping, create request with nil Params
		if method == "ping" {
			return PingRequest{BaseRequest: req, Params: nil}
		}
		// Handle nil for other types if necessary, or fall through
		fmt.Printf("Warning: NewRequest called with nil params for non-ping method %s\n", method)
	default:
		fmt.Printf("Warning: NewRequest called with unhandled params type: %T for method %s\n", params, method)
		// Returning BaseRequest is likely incorrect
	}
	// Fallback for default and nil cases (if not handled above)
	return req
}

// Helper function/type for generic response decoding later
type GenericResponse struct {
	BaseResponse
	Result json.RawMessage `json:"result,omitempty"`
}

func NewGenericResponse(id string, result json.RawMessage) GenericResponse {
	return GenericResponse{
		BaseResponse: BaseResponse{
			Jsonrpc: jsonRPCVersion,
			ID:      id,
		},
		Result: result,
	}
}

func NewGenericResponseWithErr(id string, err *ErrorObject) GenericResponse {
	return GenericResponse{
		BaseResponse: BaseResponse{
			Jsonrpc: jsonRPCVersion,
			ID:      id,
			Error:   err,
		},
	}
}

// Add conversions for full Request/Response types if needed, e.g.:

// ToProto converts the full ConnectRequest to its proto representation
func (r ConnectRequest) ToProto() *frame.ConnectPacket {
	return r.Params.ToProto()
}

// ToProto converts the full SendRequest to its proto representation
func (r SendRequest) ToProto() (*frame.SendPacket, error) {
	payloadBytes := r.Params.Payload
	pkt := &frame.SendPacket{
		Framer:      headerToFramer(r.Params.Header),
		Setting:     r.Params.Setting.ToProto(),
		ClientMsgNo: r.Params.ClientMsgNo,
		ChannelID:   r.Params.ChannelID,
		ChannelType: uint8(r.Params.ChannelType),
		Payload:     payloadBytes,
		MsgKey:      r.Params.MsgKey,
		Expire:      r.Params.Expire,
		StreamNo:    r.Params.StreamNo,
		Topic:       r.Params.Topic,
	}
	return pkt, nil
}

// Example: FromProto... for full response
func FromProtoConnackNotification(id string, ack *frame.ConnackPacket) *ConnectResponse {
	resp := &ConnectResponse{
		BaseResponse: BaseResponse{
			Jsonrpc: jsonRPCVersion,
			ID:      id,
		},
	}
	if ack.ReasonCode == frame.ReasonSuccess {
		resp.Result = FromProtoConnectAck(ack)
	} else {
		resp.Error = &ErrorObject{
			Code:    int(ack.ReasonCode),
			Message: frame.ReasonCode(ack.ReasonCode).String(),
		}
	}
	return resp
}

// Example: FromProto... for full notification
func FromProtoRecvNotification(pkt *frame.RecvPacket) RecvNotification {

	return RecvNotification{
		BaseNotification: BaseNotification{
			Jsonrpc: "2.0",
			Method:  MethodRecv,
		},
		Params: FromProtoRecvPacket(pkt),
	}
}

// NewEventNotification creates a new EventNotification
func NewEventNotification(id string, eventType string, timestamp int64, data string, header *Header) EventNotification {
	return EventNotification{
		BaseNotification: BaseNotification{
			Jsonrpc: jsonRPCVersion,
			Method:  MethodEvent,
		},
		Params: EventNotificationParams{
			Header:    header,
			ID:        id,
			Type:      eventType,
			Timestamp: timestamp,
			Data:      data,
		},
	}
}

func FromProtoEventNotification(eventPacket *frame.EventPacket) EventNotification {
	return EventNotification{
		BaseNotification: BaseNotification{
			Jsonrpc: "2.0",
			Method:  MethodEvent,
		},
		Params: EventNotificationParams{
			Header:    fromProtoHeader(eventPacket.Framer),
			ID:        eventPacket.Id,
			Type:      eventPacket.Type,
			Timestamp: eventPacket.Timestamp,
			Data:      string(eventPacket.Data),
		},
	}
}

// Similarly for DisconnectNotification...
