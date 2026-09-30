package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const mqttOriginalsRequestMagic = "WMOQ\x01"
const mqttOriginalsReplyMagic = "WMOR\x01"
const mqttOriginalsHeaderBytes = 21

var errMQTTOriginalsRPC = errors.New("channels: invalid MQTT originals RPC")

type mqttOriginalsForwardRequest struct {
	// Leader prevents a forwarded request from following a new serving node.
	Leader  ch.NodeID
	Request ch.MQTTReplayOriginalRequest
}

func encodeMQTTOriginalsRequest(q mqttOriginalsForwardRequest) ([]byte, error) {
	if !q.Request.Valid() {
		return nil, errMQTTOriginalsRPC
	}
	body, err := encodeMQTTReplayRequest(mqttReplayForwardRequest{Leader: q.Leader, Request: q.Request.Request})
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint64([]byte(mqttOriginalsRequestMagic), q.Request.StartAfter)
	b = binary.BigEndian.AppendUint64(b, q.Request.AccountedThrough)
	return append(b, body...), nil
}
func decodeMQTTOriginalsRequest(b []byte) (mqttOriginalsForwardRequest, error) {
	var empty mqttOriginalsForwardRequest
	if len(b) <= mqttOriginalsHeaderBytes || len(b) > mqttReplayRPCMaxRequestBytes+mqttOriginalsHeaderBytes || !bytes.HasPrefix(b, []byte(mqttOriginalsRequestMagic)) {
		return empty, errMQTTOriginalsRPC
	}
	base, err := decodeMQTTReplayRequest(b[mqttOriginalsHeaderBytes:])
	if err != nil {
		return empty, err
	}
	q := mqttOriginalsForwardRequest{Leader: base.Leader, Request: ch.MQTTReplayOriginalRequest{Request: base.Request, StartAfter: binary.BigEndian.Uint64(b[5:]), AccountedThrough: binary.BigEndian.Uint64(b[13:])}}
	if !q.Request.Valid() {
		return empty, errMQTTOriginalsRPC
	}
	return q, nil
}

// Reply framing reuses the frozen plan and publication-capable consumer codecs.
// The outer exact echo also binds the consumer boundary and accounted frontier.
func encodeMQTTOriginalsReply(q mqttOriginalsForwardRequest, p ch.MQTTReplayOriginalResult, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTOriginalsRequest(q)
	if err != nil {
		return nil, err
	}
	if operationErr == nil && !p.ValidFor(q.Request) {
		return nil, errMQTTOriginalsRPC
	}
	plan, err := encodeMQTTPlanReply(mqttPlanForwardRequest{Leader: q.Leader, Request: q.Request.PlanRequest()}, p.Plan, operationErr)
	if err != nil {
		return nil, err
	}
	b := binary.BigEndian.AppendUint16([]byte(mqttOriginalsReplyMagic), uint16(len(echo)))
	b = append(b, echo...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(plan)))
	b = append(b, plan...)
	if operationErr == nil {
		page, err := encodeMQTTConsumerReadReply(mqttConsumerReadForwardRequest{Leader: q.Leader, Request: ch.MQTTReplayConsumerRequest{Request: q.Request.Request, AnchorPosition: p.Plan.Anchor.Manifest.LastOffset}}, p.Page, nil)
		if err != nil {
			return nil, err
		}
		b = binary.BigEndian.AppendUint32(b, uint32(len(page)))
		b = append(b, page...)
	}
	if len(b) > mqttReplayRPCMaxReplyBytes {
		return nil, errMQTTOriginalsRPC
	}
	return b, nil
}

// readMQTTOriginalsPart returns only a bounded frame view. The nested consumer
// decoder owns its message bytes, so framing need not copy the whole page again.
func readMQTTOriginalsPart(b []byte, offset *int, max int) ([]byte, error) {
	if len(b)-*offset < 4 {
		return nil, errMQTTOriginalsRPC
	}
	n := binary.BigEndian.Uint32(b[*offset:])
	*offset += 4
	if n == 0 || uint64(n) > uint64(max) || uint64(n) > uint64(len(b)-*offset) {
		return nil, errMQTTOriginalsRPC
	}
	part := b[*offset : *offset+int(n)]
	*offset += int(n)
	return part, nil
}
func decodeMQTTOriginalsReply(b []byte, q mqttOriginalsForwardRequest) (ch.MQTTReplayOriginalResult, error) {
	var empty ch.MQTTReplayOriginalResult
	echo, err := encodeMQTTOriginalsRequest(q)
	if err != nil {
		return empty, err
	}
	if len(b) < 7+len(echo)+4 || len(b) > mqttReplayRPCMaxReplyBytes || !bytes.HasPrefix(b, []byte(mqttOriginalsReplyMagic)) || int(binary.BigEndian.Uint16(b[5:])) != len(echo) || !bytes.Equal(b[7:7+len(echo)], echo) {
		return empty, errMQTTOriginalsRPC
	}
	offset := 7 + len(echo)
	planBody, err := readMQTTOriginalsPart(b, &offset, mqttPlanRPCMaxBytes)
	if err != nil {
		return empty, err
	}
	plan, err := decodeMQTTPlanReply(planBody, mqttPlanForwardRequest{Leader: q.Leader, Request: q.Request.PlanRequest()})
	if err != nil {
		if offset != len(b) {
			return empty, errMQTTOriginalsRPC
		}
		return empty, err
	}
	if !plan.HasAnchor {
		return empty, errMQTTOriginalsRPC
	}
	pageBody, err := readMQTTOriginalsPart(b, &offset, mqttReplayRPCMaxReplyBytes)
	if err != nil || offset != len(b) {
		return empty, errMQTTOriginalsRPC
	}
	page, err := decodeMQTTConsumerReadReply(pageBody, mqttConsumerReadForwardRequest{Leader: q.Leader, Request: ch.MQTTReplayConsumerRequest{Request: q.Request.Request, AnchorPosition: plan.Anchor.Manifest.LastOffset}})
	if err != nil {
		return empty, err
	}
	result := ch.MQTTReplayOriginalResult{Plan: plan, Page: page}
	if !result.ValidFor(q.Request) {
		return empty, errMQTTOriginalsRPC
	}
	return result, nil
}
func (c *TransportClient) ForwardMQTTOriginals(ctx context.Context, node ch.NodeID, q ch.MQTTReplayOriginalRequest) (ch.MQTTReplayOriginalResult, error) {
	request := mqttOriginalsForwardRequest{Leader: node, Request: q}
	b, err := encodeMQTTOriginalsRequest(request)
	if err != nil {
		return ch.MQTTReplayOriginalResult{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTOriginals, b)
	if err != nil {
		return ch.MQTTReplayOriginalResult{}, err
	}
	return decodeMQTTOriginalsReply(reply, request)
}
func registerMQTTOriginalsHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTOriginals(context.Context, mqttOriginalsForwardRequest) (ch.MQTTReplayOriginalResult, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTOriginals, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTOriginalsRequest(b)
		if err != nil {
			return nil, err
		}
		p, err := server.handleForwardMQTTOriginals(ctx, q)
		return encodeMQTTOriginalsReply(q, p, err)
	}))
}

func (g *ServiceGateway) handleForwardMQTTOriginals(ctx context.Context, q mqttOriginalsForwardRequest) (ch.MQTTReplayOriginalResult, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayOriginalResult{}, err
	}
	return s.handleForwardMQTTOriginals(ctx, q)
}
