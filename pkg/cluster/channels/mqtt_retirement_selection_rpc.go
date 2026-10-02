package channels

import (
	"bytes"
	"context"
	"encoding/binary"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const mqttRetirementSelectionRequestMagic = "WMHQ\x01"
const mqttRetirementSelectionReplyMagic = "WMHR\x01"

func encodeMQTTRetirementSelectionRequest(q mqttRetirementSelectionForwardRequest) ([]byte, error) {
	if q.Leader == 0 || !q.Request.Valid() {
		return nil, errMQTTRetirementRPC
	}
	body, err := encodeMQTTPlanRequest(mqttPlanForwardRequest{Leader: q.Leader, Request: q.Request.Source})
	if err != nil {
		return nil, errMQTTRetirementRPC
	}
	b := appendMQTTReplayString([]byte(mqttRetirementSelectionRequestMagic), string(body))
	b, err = appendMQTTAnchorProof(b, q.Request.Captured)
	if err != nil {
		return nil, errMQTTRetirementRPC
	}
	b = binary.BigEndian.AppendUint64(b, q.Request.Through)
	b = binary.BigEndian.AppendUint64(b, q.Request.BeforeAnchor)
	b = binary.BigEndian.AppendUint16(b, uint16(q.Request.Limit))
	if len(b) > mqttRetirementRPCMaxBytes {
		return nil, errMQTTRetirementRPC
	}
	return b, nil
}

func decodeMQTTRetirementSelectionRequest(b []byte) (mqttRetirementSelectionForwardRequest, error) {
	var empty mqttRetirementSelectionForwardRequest
	if len(b) > mqttRetirementRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttRetirementSelectionRequestMagic)) {
		return empty, errMQTTRetirementRPC
	}
	r := bytes.NewReader(b[len(mqttRetirementSelectionRequestMagic):])
	body, err := readMQTTSourceString(r, mqttPlanRPCMaxBytes)
	if err != nil {
		return empty, errMQTTRetirementRPC
	}
	source, err := decodeMQTTPlanRequest([]byte(body))
	if err != nil {
		return empty, errMQTTRetirementRPC
	}
	q := mqttRetirementSelectionForwardRequest{Leader: source.Leader, Request: ch.MQTTReplayRetirementSelectionRequest{Source: source.Request}}
	q.Request.Captured, err = readMQTTAnchorProof(r)
	if err != nil {
		return empty, errMQTTRetirementRPC
	}
	var values [2]uint64
	var limit uint16
	if binary.Read(r, binary.BigEndian, &values) != nil || binary.Read(r, binary.BigEndian, &limit) != nil || r.Len() != 0 {
		return empty, errMQTTRetirementRPC
	}
	q.Request.Through, q.Request.BeforeAnchor, q.Request.Limit = values[0], values[1], int(limit)
	if !q.Request.Valid() {
		return empty, errMQTTRetirementRPC
	}
	return q, nil
}

func encodeMQTTRetirementSelectionReply(q mqttRetirementSelectionForwardRequest, p ch.MQTTReplayRetirementSelection, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTRetirementSelectionRequest(q)
	if err != nil {
		return nil, err
	}
	if operationErr == nil && !q.Request.Accepts(p) {
		return nil, errMQTTRetirementRPC
	}
	b := appendMQTTReplayString([]byte(mqttRetirementSelectionReplyMagic), string(echo))
	b = append(b, retirementRPCStatus(operationErr))
	if operationErr == nil {
		flags := byte(0)
		if p.HasCandidate {
			flags |= 1
		}
		if p.Done {
			flags |= 2
		}
		b = append(b, flags)
		b = binary.BigEndian.AppendUint64(b, p.BeforeAnchor)
		if p.HasCandidate {
			b, err = appendMQTTAnchorProof(b, p.Candidate)
			if err != nil {
				return nil, errMQTTRetirementRPC
			}
		}
	}
	if len(b) > mqttRetirementRPCMaxBytes {
		return nil, errMQTTRetirementRPC
	}
	return b, nil
}

func decodeMQTTRetirementSelectionReply(b []byte, q mqttRetirementSelectionForwardRequest) (ch.MQTTReplayRetirementSelection, error) {
	var empty ch.MQTTReplayRetirementSelection
	want, err := encodeMQTTRetirementSelectionRequest(q)
	if err != nil {
		return empty, err
	}
	r, err := readRetirementRPCReply(b, mqttRetirementSelectionReplyMagic, want)
	if err != nil {
		return empty, err
	}
	flags, err := r.ReadByte()
	if err != nil || flags&^byte(3) != 0 {
		return empty, errMQTTRetirementRPC
	}
	p := ch.MQTTReplayRetirementSelection{Captured: q.Request.Captured, HasCandidate: flags&1 != 0, Done: flags&2 != 0}
	if binary.Read(r, binary.BigEndian, &p.BeforeAnchor) != nil {
		return empty, errMQTTRetirementRPC
	}
	if p.HasCandidate {
		p.Candidate, err = readMQTTAnchorProof(r)
		if err != nil {
			return empty, errMQTTRetirementRPC
		}
	}
	if r.Len() != 0 || !q.Request.Accepts(p) {
		return empty, errMQTTRetirementRPC
	}
	return p, nil
}

func (c *TransportClient) ForwardMQTTReplayRetirementSelection(ctx context.Context, node ch.NodeID, q ch.MQTTReplayRetirementSelectionRequest) (ch.MQTTReplayRetirementSelection, error) {
	req := mqttRetirementSelectionForwardRequest{Leader: node, Request: q}
	b, err := encodeMQTTRetirementSelectionRequest(req)
	if err != nil {
		return ch.MQTTReplayRetirementSelection{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTRetirementSelection, b)
	if err != nil {
		return ch.MQTTReplayRetirementSelection{}, err
	}
	return decodeMQTTRetirementSelectionReply(reply, req)
}

func registerMQTTRetirementSelectionHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleMQTTReplayRetirementSelection(context.Context, mqttRetirementSelectionForwardRequest) (ch.MQTTReplayRetirementSelection, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTRetirementSelection, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTRetirementSelectionRequest(b)
		if err != nil {
			return nil, err
		}
		p, err := server.handleMQTTReplayRetirementSelection(ctx, q)
		return encodeMQTTRetirementSelectionReply(q, p, err)
	}))
}
