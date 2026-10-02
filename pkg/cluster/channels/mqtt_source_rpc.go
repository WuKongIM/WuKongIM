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

const mqttSourceRPCMaxBytes = 4096
const mqttSourceRequestMagic = "WMSQ\x01"
const mqttSourceReplyMagic = "WMSR\x01"

var errMQTTSourceRPC = errors.New("channels: invalid MQTT source RPC")
var errMQTTSourceRemote = errors.New("channels: MQTT source operation failed")

// Numeric positions are the closed version-1 wire status catalog. Unknown
// remote text never becomes a local sentinel or a successful source receipt.
var mqttSourceStatuses = [...]error{
	nil, ch.ErrNotLeader, ch.ErrNotReady, ch.ErrWriteFenced, ch.ErrStaleMeta,
	ch.ErrLogConflict, ch.ErrBackpressured, ch.ErrChannelNotFound,
	context.Canceled, context.DeadlineExceeded, ch.ErrInvalidConfig,
	ch.ErrNotReplica, ch.ErrClosed, ch.ErrTooManyChannels, errMQTTSourceRemote,
}

func encodeMQTTSourceRequest(req mqttSourceForwardRequest) ([]byte, error) {
	if req.Leader == 0 || !validMQTTSourceRequest(req.Request) {
		return nil, errMQTTSourceRPC
	}
	q := req.Request
	b := []byte(mqttSourceRequestMagic)
	b = binary.BigEndian.AppendUint64(b, uint64(req.Leader))
	b = binary.BigEndian.AppendUint16(b, uint16(len(q.ChannelID.ID)))
	b = append(b, q.ChannelID.ID...)
	b = append(b, byte(q.ChannelID.Type))
	for _, v := range []uint64{q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration, q.MessageID, uint64(q.ServerTimestampMS)} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	return b, nil
}

func decodeMQTTSourceRequest(b []byte) (mqttSourceForwardRequest, error) {
	var req mqttSourceForwardRequest
	if len(b) > mqttSourceRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttSourceRequestMagic)) {
		return req, errMQTTSourceRPC
	}
	r := bytes.NewReader(b[len(mqttSourceRequestMagic):])
	var leader uint64
	if binary.Read(r, binary.BigEndian, &leader) != nil {
		return req, errMQTTSourceRPC
	}
	id, err := readMQTTSourceString(r, 1024)
	if err != nil {
		return req, err
	}
	typ, err := r.ReadByte()
	if err != nil {
		return req, errMQTTSourceRPC
	}
	var values [5]uint64
	if binary.Read(r, binary.BigEndian, &values) != nil || r.Len() != 0 {
		return req, errMQTTSourceRPC
	}
	req = mqttSourceForwardRequest{Leader: ch.NodeID(leader), Request: ch.MQTTSourceRequest{
		ChannelID: ch.ChannelID{ID: id, Type: typ}, ExpectedChannelEpoch: values[0],
		ExpectedLeaderEpoch: values[1], ExpectedRouteGeneration: values[2], MessageID: values[3], ServerTimestampMS: int64(values[4]),
	}}
	if req.Leader == 0 || !validMQTTSourceRequest(req.Request) {
		return mqttSourceForwardRequest{}, errMQTTSourceRPC
	}
	return req, nil
}

func readMQTTSourceString(r *bytes.Reader, max int) (string, error) {
	var size uint16
	if binary.Read(r, binary.BigEndian, &size) != nil || int(size) > max || int(size) > r.Len() {
		return "", errMQTTSourceRPC
	}
	b := make([]byte, int(size))
	if _, err := io.ReadFull(r, b); err != nil {
		return "", errMQTTSourceRPC
	}
	return string(b), nil
}

func encodeMQTTSourceReply(req mqttSourceForwardRequest, source ch.MQTTSourceSnapshot, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTSourceRequest(req)
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
		source = ch.MQTTSourceSnapshot{}
	} else if !validMQTTSourceSnapshot(source) {
		return nil, errMQTTSourceRPC
	}
	b := []byte(mqttSourceReplyMagic)
	b = binary.BigEndian.AppendUint16(b, uint16(len(echo)))
	b = append(b, echo...)
	b = append(b, status)
	b = binary.BigEndian.AppendUint16(b, uint16(len(source.Generation)))
	b = append(b, source.Generation...)
	b = binary.BigEndian.AppendUint64(b, source.StartAfter)
	b = binary.BigEndian.AppendUint64(b, source.CommittedThrough)
	return b, nil
}

func decodeMQTTSourceReply(b []byte, req mqttSourceForwardRequest) (ch.MQTTSourceSnapshot, error) {
	if len(b) > mqttSourceRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttSourceReplyMagic)) {
		return ch.MQTTSourceSnapshot{}, errMQTTSourceRPC
	}
	r := bytes.NewReader(b[len(mqttSourceReplyMagic):])
	echo, err := readMQTTSourceString(r, mqttSourceRPCMaxBytes)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	actual, err := decodeMQTTSourceRequest([]byte(echo))
	if err != nil || actual != req {
		return ch.MQTTSourceSnapshot{}, errMQTTSourceRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return ch.MQTTSourceSnapshot{}, errMQTTSourceRPC
	}
	gen, err := readMQTTSourceString(r, 128)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	var positions [2]uint64
	if binary.Read(r, binary.BigEndian, &positions) != nil || r.Len() != 0 {
		return ch.MQTTSourceSnapshot{}, errMQTTSourceRPC
	}
	source := ch.MQTTSourceSnapshot{Generation: gen, StartAfter: positions[0], CommittedThrough: positions[1]}
	if status != 0 {
		if source != (ch.MQTTSourceSnapshot{}) {
			return ch.MQTTSourceSnapshot{}, errMQTTSourceRPC
		}
		return ch.MQTTSourceSnapshot{}, mqttSourceStatuses[status]
	}
	if !validMQTTSourceSnapshot(source) {
		return ch.MQTTSourceSnapshot{}, errMQTTSourceRPC
	}
	return source, nil
}

// ForwardMQTTSource uses the dedicated current codec with no compatibility fallback.
func (c *TransportClient) ForwardMQTTSource(ctx context.Context, node ch.NodeID, req ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
	forward := mqttSourceForwardRequest{Leader: node, Request: req}
	b, err := encodeMQTTSourceRequest(forward)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTSource, b)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	return decodeMQTTSourceReply(reply, forward)
}

func registerMQTTSourceHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTSource(context.Context, mqttSourceForwardRequest) (ch.MQTTSourceSnapshot, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTSource, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		req, err := decodeMQTTSourceRequest(b)
		if err != nil {
			return nil, err
		}
		source, err := server.handleForwardMQTTSource(ctx, req)
		return encodeMQTTSourceReply(req, source, err)
	}))
}
