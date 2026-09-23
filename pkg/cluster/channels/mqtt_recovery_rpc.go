package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const mqttRecoveryRPCMaxBytes = 4096
const mqttRecoveryRequestMagic = "WMUQ\x01"
const mqttRecoveryReplyMagic = "WMUR\x01"

var errMQTTRecoveryRPC = errors.New("channels: invalid MQTT recovery RPC")

func encodeMQTTRecoveryRequest(q ch.MQTTReplayRecoveryRequest) ([]byte, error) {
	if !q.Valid() {
		return nil, errMQTTRecoveryRPC
	}
	nested, err := encodeMQTTPlanRequest(mqttPlanForwardRequest{Leader: q.Target, Request: q.Source})
	if err != nil {
		return nil, errMQTTRecoveryRPC
	}
	b := appendMQTTReplayString([]byte(mqttRecoveryRequestMagic), string(nested))
	for _, v := range []uint64{q.TargetAnchor, q.AfterAnchor, uint64(q.DonorAfter)} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	b = append(b, byte(q.ScanLimit))
	if len(b) > mqttRecoveryRPCMaxBytes {
		return nil, errMQTTRecoveryRPC
	}
	return b, nil
}
func decodeMQTTRecoveryRequest(b []byte) (ch.MQTTReplayRecoveryRequest, error) {
	var empty ch.MQTTReplayRecoveryRequest
	if len(b) > mqttRecoveryRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttRecoveryRequestMagic)) {
		return empty, errMQTTRecoveryRPC
	}
	r := bytes.NewReader(b[len(mqttRecoveryRequestMagic):])
	nested, err := readMQTTSourceString(r, mqttRecoveryRPCMaxBytes)
	if err != nil {
		return empty, errMQTTRecoveryRPC
	}
	inner, err := decodeMQTTPlanRequest([]byte(nested))
	if err != nil {
		return empty, errMQTTRecoveryRPC
	}
	var fields [3]uint64
	if binary.Read(r, binary.BigEndian, &fields) != nil {
		return empty, errMQTTRecoveryRPC
	}
	limit, err := r.ReadByte()
	if err != nil || r.Len() != 0 {
		return empty, errMQTTRecoveryRPC
	}
	q := ch.MQTTReplayRecoveryRequest{Target: inner.Leader, Source: inner.Request, TargetAnchor: fields[0], AfterAnchor: fields[1], DonorAfter: ch.NodeID(fields[2]), ScanLimit: int(limit)}
	if !q.Valid() {
		return empty, errMQTTRecoveryRPC
	}
	return q, nil
}

// Recovery replies carry only proofs and bounded continuation hints. Page bodies
// stay in RPC 98, whose receiver binds them to its independently committed anchor.
func encodeMQTTRecoveryReply(q ch.MQTTReplayRecoveryRequest, p ch.MQTTReplayRecoveryResult, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTRecoveryRequest(q)
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
	} else if !p.ValidFor(q) {
		return nil, errMQTTRecoveryRPC
	}
	b := appendMQTTReplayString([]byte(mqttRecoveryReplyMagic), string(echo))
	b = append(b, status)
	if status != 0 {
		return b, nil
	}
	b = appendMQTTReplayPrefix(b, p.Plan.Current)
	b, err = appendMQTTAnchorProof(b, p.Plan.Target)
	if err != nil {
		return nil, errMQTTRecoveryRPC
	}
	flags := byte(0)
	if p.Plan.HasNext {
		flags |= 1
	}
	if p.Plan.Complete {
		flags |= 2
	}
	if p.Repaired {
		flags |= 4
	}
	b = append(b, flags)
	b = binary.BigEndian.AppendUint64(b, p.Plan.ScanAfter)
	b = binary.BigEndian.AppendUint64(b, uint64(p.DonorAfter))
	if p.Plan.HasNext {
		b, err = appendMQTTAnchorProof(b, p.Plan.Next)
		if err != nil {
			return nil, errMQTTRecoveryRPC
		}
	}
	if len(b) > mqttRecoveryRPCMaxBytes {
		return nil, errMQTTRecoveryRPC
	}
	return b, nil
}
func decodeMQTTRecoveryReply(b []byte, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	var empty ch.MQTTReplayRecoveryResult
	if len(b) > mqttRecoveryRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttRecoveryReplyMagic)) {
		return empty, errMQTTRecoveryRPC
	}
	r := bytes.NewReader(b[len(mqttRecoveryReplyMagic):])
	echo, err := readMQTTSourceString(r, mqttRecoveryRPCMaxBytes)
	if err != nil {
		return empty, errMQTTRecoveryRPC
	}
	want, err := encodeMQTTRecoveryRequest(q)
	if err != nil || !bytes.Equal([]byte(echo), want) {
		return empty, errMQTTRecoveryRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return empty, errMQTTRecoveryRPC
	}
	if status != 0 {
		if r.Len() != 0 {
			return empty, errMQTTRecoveryRPC
		}
		return empty, mqttSourceStatuses[status]
	}
	var out ch.MQTTReplayRecoveryResult
	out.Plan.Current, err = readMQTTReplayPrefix(r)
	if err != nil {
		return empty, errMQTTRecoveryRPC
	}
	out.Plan.Target, err = readMQTTAnchorProof(r)
	if err != nil {
		return empty, errMQTTRecoveryRPC
	}
	flags, err := r.ReadByte()
	if err != nil || flags & ^byte(7) != 0 {
		return empty, errMQTTRecoveryRPC
	}
	out.Plan.HasNext = flags&1 != 0
	out.Plan.Complete = flags&2 != 0
	out.Repaired = flags&4 != 0
	var fields [2]uint64
	if binary.Read(r, binary.BigEndian, &fields) != nil {
		return empty, errMQTTRecoveryRPC
	}
	out.Plan.ScanAfter = fields[0]
	out.DonorAfter = ch.NodeID(fields[1])
	if out.Plan.HasNext {
		out.Plan.Next, err = readMQTTAnchorProof(r)
		if err != nil {
			return empty, errMQTTRecoveryRPC
		}
	}
	if r.Len() != 0 || !out.ValidFor(q) {
		return empty, errMQTTRecoveryRPC
	}
	return out, nil
}

// ForwardMQTTReplayRecoveryStep invokes one exact receiver without rerouting.
func (c *TransportClient) ForwardMQTTReplayRecoveryStep(ctx context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	b, err := encodeMQTTRecoveryRequest(q)
	if err != nil {
		return ch.MQTTReplayRecoveryResult{}, err
	}
	reply, err := c.call(ctx, uint64(q.Target), clusternet.RPCChannelMQTTRecovery, b)
	if err != nil {
		return ch.MQTTReplayRecoveryResult{}, err
	}
	return decodeMQTTRecoveryReply(reply, q)
}
func registerMQTTRecoveryHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleMQTTReplayRecoveryStep(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTRecovery, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTRecoveryRequest(b)
		if err != nil {
			return nil, err
		}
		result, err := server.handleMQTTReplayRecoveryStep(ctx, q)
		return encodeMQTTRecoveryReply(q, result, err)
	}))
}
