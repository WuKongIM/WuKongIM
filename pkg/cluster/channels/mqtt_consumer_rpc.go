package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const mqttConsumerRequestMagic = "WMCQ\x01"
const mqttConsumerReplyMagic = "WMCR\x01"
const mqttConsumerHeaderBytes = 13

var errMQTTConsumerRPC = errors.New("channels: invalid MQTT consumer read RPC")

func encodeMQTTConsumerReadRequest(q mqttConsumerReadForwardRequest) ([]byte, error) {
	if !q.Request.Valid() {
		return nil, errMQTTConsumerRPC
	}
	body, err := encodeMQTTReplayRequest(mqttReplayForwardRequest{Leader: q.Leader, Request: q.Request.Request})
	if err != nil {
		return nil, err
	}
	return append(binary.BigEndian.AppendUint64([]byte(mqttConsumerRequestMagic), q.Request.AnchorPosition), body...), nil
}
func decodeMQTTConsumerReadRequest(b []byte) (mqttConsumerReadForwardRequest, error) {
	var empty mqttConsumerReadForwardRequest
	if len(b) <= mqttConsumerHeaderBytes || len(b) > mqttReplayRPCMaxRequestBytes+mqttConsumerHeaderBytes || !bytes.HasPrefix(b, []byte(mqttConsumerRequestMagic)) {
		return empty, errMQTTConsumerRPC
	}
	base, err := decodeMQTTReplayRequest(b[mqttConsumerHeaderBytes:])
	if err != nil {
		return empty, err
	}
	q := mqttConsumerReadForwardRequest{Leader: base.Leader, Request: ch.MQTTReplayConsumerRequest{Request: base.Request, AnchorPosition: binary.BigEndian.Uint64(b[len(mqttConsumerRequestMagic):])}}
	if !q.Request.Valid() {
		return empty, errMQTTConsumerRPC
	}
	return q, nil
}
func encodeMQTTConsumerReadReply(q mqttConsumerReadForwardRequest, p ch.MQTTReplayPage, operationErr error) ([]byte, error) {
	if !q.Request.Valid() {
		return nil, errMQTTConsumerRPC
	}
	body, err := encodeMQTTReplayReply(mqttReplayForwardRequest{Leader: q.Leader, Request: q.Request.Request}, p, operationErr)
	if err != nil {
		return nil, err
	}
	return append(binary.BigEndian.AppendUint64([]byte(mqttConsumerReplyMagic), q.Request.AnchorPosition), body...), nil
}
func decodeMQTTConsumerReadReply(b []byte, q mqttConsumerReadForwardRequest) (ch.MQTTReplayPage, error) {
	if !q.Request.Valid() || len(b) <= mqttConsumerHeaderBytes || len(b) > mqttReplayRPCMaxReplyBytes+mqttConsumerHeaderBytes || !bytes.HasPrefix(b, []byte(mqttConsumerReplyMagic)) || binary.BigEndian.Uint64(b[len(mqttConsumerReplyMagic):]) != q.Request.AnchorPosition {
		return ch.MQTTReplayPage{}, errMQTTConsumerRPC
	}
	return decodeMQTTReplayReply(b[mqttConsumerHeaderBytes:], mqttReplayForwardRequest{Leader: q.Leader, Request: q.Request.Request})
}

func (c *TransportClient) ForwardMQTTConsumerRead(ctx context.Context, node ch.NodeID, q ch.MQTTReplayConsumerRequest) (ch.MQTTReplayPage, error) {
	request := mqttConsumerReadForwardRequest{Leader: node, Request: q}
	b, err := encodeMQTTConsumerReadRequest(request)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTConsumerRead, b)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	return decodeMQTTConsumerReadReply(reply, request)
}
func registerMQTTConsumerReadHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTConsumerRead(context.Context, mqttConsumerReadForwardRequest) (ch.MQTTReplayPage, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTConsumerRead, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTConsumerReadRequest(b)
		if err != nil {
			return nil, err
		}
		p, err := server.handleForwardMQTTConsumerRead(ctx, q)
		return encodeMQTTConsumerReadReply(q, p, err)
	}))
}
