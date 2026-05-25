package channels

import (
	"encoding/json"
	"fmt"

	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

const codecVersion = uint8(1)

const (
	kindPull uint8 = iota + 1
	kindPullResponse
	kindAck
	kindPullHint
	kindNotify
)

// EncodePullRequest encodes a ChannelV2 pull request.
func EncodePullRequest(req channeltransport.PullRequest) ([]byte, error) {
	return encode(kindPull, req)
}

// DecodePullRequest decodes a ChannelV2 pull request.
func DecodePullRequest(data []byte) (channeltransport.PullRequest, error) {
	var req channeltransport.PullRequest
	return req, decode(data, kindPull, &req)
}

func encodePullResponse(resp channeltransport.PullResponse) ([]byte, error) {
	return encode(kindPullResponse, resp)
}
func decodePullResponse(data []byte) (channeltransport.PullResponse, error) {
	var resp channeltransport.PullResponse
	return resp, decode(data, kindPullResponse, &resp)
}
func encodeAckRequest(req channeltransport.AckRequest) ([]byte, error) { return encode(kindAck, req) }
func decodeAckRequest(data []byte) (channeltransport.AckRequest, error) {
	var req channeltransport.AckRequest
	return req, decode(data, kindAck, &req)
}
func encodePullHintRequest(req channeltransport.PullHintRequest) ([]byte, error) {
	return encode(kindPullHint, req)
}
func decodePullHintRequest(data []byte) (channeltransport.PullHintRequest, error) {
	var req channeltransport.PullHintRequest
	return req, decode(data, kindPullHint, &req)
}
func encodeNotifyRequest(req channeltransport.NotifyRequest) ([]byte, error) {
	return encode(kindNotify, req)
}
func decodeNotifyRequest(data []byte) (channeltransport.NotifyRequest, error) {
	var req channeltransport.NotifyRequest
	return req, decode(data, kindNotify, &req)
}

func encode(kind uint8, value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	out := []byte{codecVersion, kind}
	return append(out, payload...), nil
}

func decode(data []byte, wantKind uint8, value any) error {
	if len(data) < 2 || data[0] != codecVersion || data[1] != wantKind {
		return fmt.Errorf("channels: invalid frame")
	}
	return json.Unmarshal(data[2:], value)
}
