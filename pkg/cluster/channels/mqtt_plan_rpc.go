package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const mqttPlanRPCMaxBytes = 4096
const mqttPlanRequestMagic = "WMPQ\x01"
const mqttPlanReplyMagic = "WMPR\x01"

var errMQTTPlanRPC = errors.New("channels: invalid MQTT plan RPC")

func encodeMQTTPlanRequest(q mqttPlanForwardRequest) ([]byte, error) {
	if q.Leader == 0 || !q.Request.Valid() {
		return nil, errMQTTPlanRPC
	}
	r := q.Request
	b := binary.BigEndian.AppendUint64([]byte(mqttPlanRequestMagic), uint64(q.Leader))
	b = appendMQTTReplayString(b, r.ChannelID.ID)
	b = append(b, r.ChannelID.Type)
	for _, v := range []uint64{r.ExpectedChannelEpoch, r.ExpectedLeaderEpoch, r.ExpectedRouteGeneration} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	return appendMQTTReplayString(b, r.Generation), nil
}

func decodeMQTTPlanRequest(b []byte) (mqttPlanForwardRequest, error) {
	var empty mqttPlanForwardRequest
	if len(b) > mqttPlanRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttPlanRequestMagic)) {
		return empty, errMQTTPlanRPC
	}
	r := bytes.NewReader(b[len(mqttPlanRequestMagic):])
	var leader uint64
	if binary.Read(r, binary.BigEndian, &leader) != nil {
		return empty, errMQTTPlanRPC
	}
	id, err := readMQTTSourceString(r, 1024)
	if err != nil {
		return empty, errMQTTPlanRPC
	}
	typ, err := r.ReadByte()
	if err != nil {
		return empty, errMQTTPlanRPC
	}
	var fences [3]uint64
	if binary.Read(r, binary.BigEndian, &fences) != nil {
		return empty, errMQTTPlanRPC
	}
	gen, err := readMQTTSourceString(r, 128)
	if err != nil || r.Len() != 0 {
		return empty, errMQTTPlanRPC
	}
	q := mqttPlanForwardRequest{Leader: ch.NodeID(leader), Request: ch.MQTTReplayPlanRequest{ChannelID: ch.ChannelID{ID: id, Type: typ}, ExpectedChannelEpoch: fences[0], ExpectedLeaderEpoch: fences[1], ExpectedRouteGeneration: fences[2], Generation: gen}}
	if q.Leader == 0 || !q.Request.Valid() {
		return empty, errMQTTPlanRPC
	}
	return q, nil
}

func encodeMQTTPlanReply(q mqttPlanForwardRequest, p ch.MQTTReplayPlan, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTPlanRequest(q)
	if err != nil {
		return nil, err
	}
	status := byte(0)
	if operationErr != nil {
		status = byte(len(mqttSourceStatuses) - 1)
		for i := 1; i < len(mqttSourceStatuses)-1; i++ {
			if errors.Is(operationErr, mqttSourceStatuses[i]) {
				status = byte(i)
				break
			}
		}
	} else if !p.ValidFor(q.Request) {
		return nil, errMQTTPlanRPC
	}
	b := appendMQTTReplayString([]byte(mqttPlanReplyMagic), string(echo))
	b = append(b, status)
	if operationErr == nil {
		b = appendMQTTReplayString(b, p.Source.Generation)
		b = binary.BigEndian.AppendUint64(b, p.Source.StartAfter)
		b = binary.BigEndian.AppendUint64(b, p.Source.CommittedThrough)
		if p.HasAnchor {
			b = append(b, 1)
			b, err = appendMQTTAnchorProof(b, p.Anchor)
			if err != nil {
				return nil, errMQTTPlanRPC
			}
		} else {
			b = append(b, 0)
		}
	}
	if len(b) > mqttPlanRPCMaxBytes {
		return nil, errMQTTPlanRPC
	}
	return b, nil
}

func decodeMQTTPlanReply(b []byte, q mqttPlanForwardRequest) (ch.MQTTReplayPlan, error) {
	var empty ch.MQTTReplayPlan
	if len(b) > mqttPlanRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttPlanReplyMagic)) {
		return empty, errMQTTPlanRPC
	}
	r := bytes.NewReader(b[len(mqttPlanReplyMagic):])
	echo, err := readMQTTSourceString(r, mqttPlanRPCMaxBytes)
	if err != nil {
		return empty, errMQTTPlanRPC
	}
	want, err := encodeMQTTPlanRequest(q)
	if err != nil || !bytes.Equal([]byte(echo), want) {
		return empty, errMQTTPlanRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return empty, errMQTTPlanRPC
	}
	if status != 0 {
		if r.Len() != 0 {
			return empty, errMQTTPlanRPC
		}
		return empty, mqttSourceStatuses[status]
	}
	gen, err := readMQTTSourceString(r, 128)
	if err != nil {
		return empty, errMQTTPlanRPC
	}
	var positions [2]uint64
	if binary.Read(r, binary.BigEndian, &positions) != nil {
		return empty, errMQTTPlanRPC
	}
	has, err := r.ReadByte()
	if err != nil || has > 1 {
		return empty, errMQTTPlanRPC
	}
	p := ch.MQTTReplayPlan{Source: ch.MQTTSourceSnapshot{Generation: gen, StartAfter: positions[0], CommittedThrough: positions[1]}, HasAnchor: has == 1}
	if p.HasAnchor {
		p.Anchor, err = readMQTTAnchorProof(r)
		if err != nil {
			return empty, errMQTTPlanRPC
		}
	}
	if r.Len() != 0 || !p.ValidFor(q.Request) {
		return empty, errMQTTPlanRPC
	}
	return p, nil
}

// ForwardMQTTPlan sends the exact source/fences to one current serving node.
func (c *TransportClient) ForwardMQTTPlan(ctx context.Context, node ch.NodeID, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	forward := mqttPlanForwardRequest{Leader: node, Request: q}
	b, err := encodeMQTTPlanRequest(forward)
	if err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTPlan, b)
	if err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	return decodeMQTTPlanReply(reply, forward)
}

func registerMQTTPlanHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTPlan(context.Context, mqttPlanForwardRequest) (ch.MQTTReplayPlan, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTPlan, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTPlanRequest(b)
		if err != nil {
			return nil, err
		}
		p, err := server.handleForwardMQTTPlan(ctx, q)
		return encodeMQTTPlanReply(q, p, err)
	}))
}
