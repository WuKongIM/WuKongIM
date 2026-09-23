package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const mqttCopyRPCMaxBytes = 4096
const mqttCopyRequestMagic = "WMCQ\x01"
const mqttCopyReplyMagic = "WMCR\x01"

var errMQTTCopyRPC = errors.New("channels: invalid MQTT copy RPC")

func encodeMQTTCopyRequest(q mqttCopyRequest) ([]byte, error) {
	if !q.valid() {
		return nil, errMQTTCopyRPC
	}
	replay, err := encodeMQTTReplayRequest(mqttReplayForwardRequest{Leader: q.Leader, Request: q.Request})
	if err != nil {
		return nil, errMQTTCopyRPC
	}
	b := binary.BigEndian.AppendUint64([]byte(mqttCopyRequestMagic), uint64(q.Target))
	b = appendMQTTReplayString(b, string(replay))
	b = append(b, q.Authority[:]...)
	b = appendMQTTReplayPrefix(b, q.Before)
	b = appendMQTTReplayPrefix(b, q.After)
	if len(b) > mqttCopyRPCMaxBytes {
		return nil, errMQTTCopyRPC
	}
	return b, nil
}

func decodeMQTTCopyRequest(b []byte) (mqttCopyRequest, error) {
	var q mqttCopyRequest
	if len(b) > mqttCopyRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttCopyRequestMagic)) {
		return q, errMQTTCopyRPC
	}
	r := bytes.NewReader(b[len(mqttCopyRequestMagic):])
	var target uint64
	if binary.Read(r, binary.BigEndian, &target) != nil {
		return q, errMQTTCopyRPC
	}
	replay, err := readMQTTSourceString(r, mqttCopyRPCMaxBytes)
	if err != nil {
		return q, errMQTTCopyRPC
	}
	req, err := decodeMQTTReplayRequest([]byte(replay))
	if err != nil {
		return q, errMQTTCopyRPC
	}
	q.Target, q.Leader, q.Request = ch.NodeID(target), req.Leader, req.Request
	if _, err = io.ReadFull(r, q.Authority[:]); err != nil {
		return mqttCopyRequest{}, errMQTTCopyRPC
	}
	q.Before, err = readMQTTReplayPrefix(r)
	if err != nil {
		return mqttCopyRequest{}, errMQTTCopyRPC
	}
	q.After, err = readMQTTReplayPrefix(r)
	if err != nil || r.Len() != 0 || !q.valid() {
		return mqttCopyRequest{}, errMQTTCopyRPC
	}
	return q, nil
}

func encodeMQTTCopyReply(q mqttCopyRequest, ack ch.NodeID, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTCopyRequest(q)
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
	} else if ack != q.Target {
		return nil, errMQTTCopyRPC
	}
	b := appendMQTTReplayString([]byte(mqttCopyReplyMagic), string(echo))
	b = append(b, status)
	if operationErr == nil {
		b = binary.BigEndian.AppendUint64(b, uint64(ack))
	}
	if len(b) > mqttCopyRPCMaxBytes {
		return nil, errMQTTCopyRPC
	}
	return b, nil
}

func decodeMQTTCopyReply(b []byte, q mqttCopyRequest) (ch.NodeID, error) {
	if len(b) > mqttCopyRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttCopyReplyMagic)) {
		return 0, errMQTTCopyRPC
	}
	r := bytes.NewReader(b[len(mqttCopyReplyMagic):])
	echo, err := readMQTTSourceString(r, mqttCopyRPCMaxBytes)
	if err != nil {
		return 0, errMQTTCopyRPC
	}
	actual, err := decodeMQTTCopyRequest([]byte(echo))
	if err != nil || actual != q {
		return 0, errMQTTCopyRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return 0, errMQTTCopyRPC
	}
	if status != 0 {
		if r.Len() != 0 {
			return 0, errMQTTCopyRPC
		}
		return 0, mqttSourceStatuses[status]
	}
	var ack uint64
	if binary.Read(r, binary.BigEndian, &ack) != nil || r.Len() != 0 || ch.NodeID(ack) != q.Target {
		return 0, errMQTTCopyRPC
	}
	return ch.NodeID(ack), nil
}

// ConfirmMQTTReplayCopy asks exactly one current voter for independently derived
// durable coverage; the body-free request and complete echo remain bounded.
func (c *TransportClient) ConfirmMQTTReplayCopy(ctx context.Context, node ch.NodeID, q mqttCopyRequest) (ch.NodeID, error) {
	if node != q.Target {
		return 0, errMQTTCopyRPC
	}
	b, err := encodeMQTTCopyRequest(q)
	if err != nil {
		return 0, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTCopy, b)
	if err != nil {
		return 0, err
	}
	return decodeMQTTCopyReply(reply, q)
}

func registerMQTTCopyHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		confirmMQTTReplayCopy(context.Context, mqttCopyRequest) (ch.NodeID, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTCopy, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTCopyRequest(b)
		if err != nil {
			return nil, err
		}
		ack, err := server.confirmMQTTReplayCopy(ctx, q)
		return encodeMQTTCopyReply(q, ack, err)
	}))
}
