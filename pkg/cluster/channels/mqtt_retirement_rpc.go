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

const mqttRetirementRPCMaxBytes = 8 << 10
const mqttRetirementCommitRequestMagic = "WMTQ\x01"
const mqttRetirementCommitReplyMagic = "WMTR\x01"

var errMQTTRetirementRPC = errors.New("channels: invalid MQTT retirement RPC")

func encodeMQTTRetirementRequest(q mqttRetirementForwardRequest) ([]byte, error) {
	if !q.valid() {
		return nil, errMQTTRetirementRPC
	}
	selection, err := encodeMQTTRetirementSelectionRequest(mqttRetirementSelectionForwardRequest{Leader: q.Leader, Request: q.Selection})
	if err != nil {
		return nil, err
	}
	b := appendMQTTReplayString([]byte(mqttRetirementCommitRequestMagic), string(selection))
	b = append(b, q.Authority[:]...)
	b, err = appendMQTTAnchorProof(b, q.Candidate)
	if err != nil {
		return nil, errMQTTRetirementRPC
	}
	b = binary.BigEndian.AppendUint64(b, q.MessageID)
	b = binary.BigEndian.AppendUint64(b, uint64(q.ServerTimestampMS))
	if len(b) > mqttRetirementRPCMaxBytes {
		return nil, errMQTTRetirementRPC
	}
	return b, nil
}

func decodeMQTTRetirementRequest(b []byte) (mqttRetirementForwardRequest, error) {
	var empty mqttRetirementForwardRequest
	if len(b) > mqttRetirementRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttRetirementCommitRequestMagic)) {
		return empty, errMQTTRetirementRPC
	}
	r := bytes.NewReader(b[len(mqttRetirementCommitRequestMagic):])
	body, err := readMQTTSourceString(r, mqttRetirementRPCMaxBytes)
	if err != nil {
		return empty, errMQTTRetirementRPC
	}
	selection, err := decodeMQTTRetirementSelectionRequest([]byte(body))
	if err != nil {
		return empty, errMQTTRetirementRPC
	}
	q := mqttRetirementForwardRequest{Leader: selection.Leader, Selection: selection.Request}
	if _, err = io.ReadFull(r, q.Authority[:]); err != nil {
		return empty, errMQTTRetirementRPC
	}
	q.Candidate, err = readMQTTAnchorProof(r)
	if err != nil {
		return empty, errMQTTRetirementRPC
	}
	var values [2]uint64
	if binary.Read(r, binary.BigEndian, &values) != nil || r.Len() != 0 {
		return empty, errMQTTRetirementRPC
	}
	q.MessageID, q.ServerTimestampMS = values[0], int64(values[1])
	if !q.valid() {
		return empty, errMQTTRetirementRPC
	}
	return q, nil
}

// retirementRPCStatus reuses the closed source status catalog without exposing
// remote text as a sentinel. Both new envelopes keep identical error framing.
func retirementRPCStatus(operationErr error) byte {
	if operationErr == nil {
		return 0
	}
	for i := 1; i < len(mqttSourceStatuses)-1; i++ {
		if errors.Is(operationErr, mqttSourceStatuses[i]) {
			return byte(i)
		}
	}
	return byte(len(mqttSourceStatuses) - 1)
}

func encodeMQTTRetirementReply(q mqttRetirementForwardRequest, p ch.MQTTReplayRetirementProof, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTRetirementRequest(q)
	if err != nil {
		return nil, err
	}
	if operationErr == nil && !q.accepts(p) {
		return nil, errMQTTRetirementRPC
	}
	b := appendMQTTReplayString([]byte(mqttRetirementCommitReplyMagic), string(echo))
	b = append(b, retirementRPCStatus(operationErr))
	if operationErr == nil {
		b, err = appendMQTTAnchorProof(b, ch.MQTTReplayAnchorProof{Anchor: p.Retirement.Anchor, Manifest: p.Manifest})
		if err != nil {
			return nil, errMQTTRetirementRPC
		}
		b = binary.BigEndian.AppendUint64(b, p.Retirement.AnchorPosition)
		b = append(b, p.Retirement.AnchorDigest[:]...)
	}
	if len(b) > mqttRetirementRPCMaxBytes {
		return nil, errMQTTRetirementRPC
	}
	return b, nil
}

// readRetirementRPCReply validates magic, exact request echo and error framing
// before a caller reads its one bounded success payload.
func readRetirementRPCReply(b []byte, magic string, want []byte) (*bytes.Reader, error) {
	if len(b) > mqttRetirementRPCMaxBytes || !bytes.HasPrefix(b, []byte(magic)) {
		return nil, errMQTTRetirementRPC
	}
	r := bytes.NewReader(b[len(magic):])
	echo, err := readMQTTSourceString(r, mqttRetirementRPCMaxBytes)
	if err != nil || !bytes.Equal([]byte(echo), want) {
		return nil, errMQTTRetirementRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return nil, errMQTTRetirementRPC
	}
	if status != 0 {
		if r.Len() != 0 {
			return nil, errMQTTRetirementRPC
		}
		return nil, mqttSourceStatuses[status]
	}
	return r, nil
}

func decodeMQTTRetirementReply(b []byte, q mqttRetirementForwardRequest) (ch.MQTTReplayRetirementProof, error) {
	var empty ch.MQTTReplayRetirementProof
	want, err := encodeMQTTRetirementRequest(q)
	if err != nil {
		return empty, err
	}
	r, err := readRetirementRPCReply(b, mqttRetirementCommitReplyMagic, want)
	if err != nil {
		return empty, err
	}
	a, err := readMQTTAnchorProof(r)
	if err != nil {
		return empty, errMQTTRetirementRPC
	}
	p := ch.MQTTReplayRetirementProof{Manifest: a.Manifest}
	p.Retirement.Anchor = a.Anchor
	if binary.Read(r, binary.BigEndian, &p.Retirement.AnchorPosition) != nil {
		return empty, errMQTTRetirementRPC
	}
	if _, err = io.ReadFull(r, p.Retirement.AnchorDigest[:]); err != nil || r.Len() != 0 || !q.accepts(p) {
		return empty, errMQTTRetirementRPC
	}
	return p, nil
}

func (c *TransportClient) ForwardMQTTReplayRetirement(ctx context.Context, node ch.NodeID, q mqttRetirementForwardRequest) (ch.MQTTReplayRetirementProof, error) {
	if node != q.Leader {
		return ch.MQTTReplayRetirementProof{}, errMQTTRetirementRPC
	}
	b, err := encodeMQTTRetirementRequest(q)
	if err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTRetirement, b)
	if err != nil {
		return ch.MQTTReplayRetirementProof{}, err
	}
	return decodeMQTTRetirementReply(reply, q)
}

func registerMQTTRetirementHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTReplayRetirement(context.Context, mqttRetirementForwardRequest) (ch.MQTTReplayRetirementProof, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTRetirement, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTRetirementRequest(b)
		if err != nil {
			return nil, err
		}
		p, err := server.handleForwardMQTTReplayRetirement(ctx, q)
		return encodeMQTTRetirementReply(q, p, err)
	}))
}
