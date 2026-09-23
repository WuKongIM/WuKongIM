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

const (
	mqttReplayRPCMaxRequestBytes = 4096
	mqttReplayRPCMaxReplyBytes   = (16 << 20) + (64 << 10)
	mqttReplayRequestMagic       = "WMRQ\x01"
	mqttReplayReplyMagic         = "WMRR\x01"
)

var errMQTTReplayRPC = errors.New("channels: invalid MQTT replay RPC")

func encodeMQTTReplayRequest(req mqttReplayForwardRequest) ([]byte, error) {
	if req.Leader == 0 || !validMQTTReplayRequest(req.Request) {
		return nil, errMQTTReplayRPC
	}
	q := req.Request
	b := []byte(mqttReplayRequestMagic)
	b = binary.BigEndian.AppendUint64(b, uint64(req.Leader))
	b = appendMQTTReplayString(b, q.ChannelID.ID)
	b = append(b, byte(q.ChannelID.Type))
	for _, v := range []uint64{q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	b = appendMQTTReplayString(b, q.Range.Generation)
	b = binary.BigEndian.AppendUint64(b, q.Range.From)
	b = binary.BigEndian.AppendUint64(b, q.Range.Through)
	b = binary.BigEndian.AppendUint16(b, uint16(q.Range.Limit))
	b = binary.BigEndian.AppendUint32(b, uint32(q.Range.MaxBytes))
	return b, nil
}

func decodeMQTTReplayRequest(b []byte) (mqttReplayForwardRequest, error) {
	var req mqttReplayForwardRequest
	if len(b) > mqttReplayRPCMaxRequestBytes || !bytes.HasPrefix(b, []byte(mqttReplayRequestMagic)) {
		return req, errMQTTReplayRPC
	}
	r := bytes.NewReader(b[len(mqttReplayRequestMagic):])
	var leader uint64
	if binary.Read(r, binary.BigEndian, &leader) != nil {
		return req, errMQTTReplayRPC
	}
	id, err := readMQTTSourceString(r, 1024)
	if err != nil {
		return req, errMQTTReplayRPC
	}
	typ, err := r.ReadByte()
	if err != nil {
		return req, errMQTTReplayRPC
	}
	var fences [3]uint64
	if binary.Read(r, binary.BigEndian, &fences) != nil {
		return req, errMQTTReplayRPC
	}
	gen, err := readMQTTSourceString(r, 128)
	if err != nil {
		return req, errMQTTReplayRPC
	}
	var positions [2]uint64
	var limit uint16
	var budget uint32
	if binary.Read(r, binary.BigEndian, &positions) != nil || binary.Read(r, binary.BigEndian, &limit) != nil || binary.Read(r, binary.BigEndian, &budget) != nil || r.Len() != 0 {
		return req, errMQTTReplayRPC
	}
	req = mqttReplayForwardRequest{Leader: ch.NodeID(leader), Request: ch.MQTTReplayRequest{ChannelID: ch.ChannelID{ID: id, Type: typ},
		ExpectedChannelEpoch: fences[0], ExpectedLeaderEpoch: fences[1], ExpectedRouteGeneration: fences[2],
		Range: ch.MQTTReplayRange{Generation: gen, From: positions[0], Through: positions[1], Limit: int(limit), MaxBytes: int(budget)}}}
	if req.Leader == 0 || !validMQTTReplayRequest(req.Request) {
		return mqttReplayForwardRequest{}, errMQTTReplayRPC
	}
	return req, nil
}

func appendMQTTReplayString(b []byte, value string) []byte {
	b = binary.BigEndian.AppendUint16(b, uint16(len(value)))
	return append(b, value...)
}

func appendMQTTReplayPrefix(b []byte, p ch.MQTTReplayPrefix) []byte {
	b = appendMQTTReplayString(b, p.Generation)
	for _, v := range []uint64{p.StartAfter, p.Through, p.TotalBytes, p.TotalStoredBytes} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	return append(b, p.Digest[:]...)
}

func readMQTTReplayPrefix(r *bytes.Reader) (ch.MQTTReplayPrefix, error) {
	gen, err := readMQTTSourceString(r, 128)
	if err != nil {
		return ch.MQTTReplayPrefix{}, errMQTTReplayRPC
	}
	var values [4]uint64
	var digest [32]byte
	if binary.Read(r, binary.BigEndian, &values) != nil {
		return ch.MQTTReplayPrefix{}, errMQTTReplayRPC
	}
	if _, err := io.ReadFull(r, digest[:]); err != nil {
		return ch.MQTTReplayPrefix{}, errMQTTReplayRPC
	}
	return ch.MQTTReplayPrefix{Generation: gen, StartAfter: values[0], Through: values[1], TotalBytes: values[2], TotalStoredBytes: values[3], Digest: digest}, nil
}

// Version 1 reuses the frozen MQTT source status catalog. Error replies omit
// page data entirely; arbitrary remote text cannot become a successful result.
func encodeMQTTReplayReply(req mqttReplayForwardRequest, page ch.MQTTReplayPage, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTReplayRequest(req)
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
	} else if !page.ValidFor(req.Request.Range) {
		return nil, errMQTTReplayRPC
	}
	b := []byte(mqttReplayReplyMagic)
	b = binary.BigEndian.AppendUint16(b, uint16(len(echo)))
	b = append(b, echo...)
	b = append(b, status)
	if status != 0 {
		return b, nil
	}
	b = appendMQTTReplayPrefix(b, page.Before)
	b = appendMQTTReplayPrefix(b, page.After)
	b = binary.BigEndian.AppendUint16(b, uint16(len(page.Records)))
	for _, record := range page.Records {
		for _, v := range []uint64{record.Position, record.ContentVersion, record.MessageID, record.AccountedBytes, record.TotalBytes, record.TotalStoredBytes} {
			b = binary.BigEndian.AppendUint64(b, v)
		}
		b = append(b, record.ContentHash[:]...)
		b = append(b, record.Digest[:]...)
		b = binary.BigEndian.AppendUint32(b, uint32(len(record.Content)))
		b = append(b, record.Content...)
	}
	if len(b) > mqttReplayRPCMaxReplyBytes {
		return nil, errMQTTReplayRPC
	}
	return b, nil
}

func decodeMQTTReplayReply(b []byte, req mqttReplayForwardRequest) (ch.MQTTReplayPage, error) {
	if len(b) > mqttReplayRPCMaxReplyBytes || !bytes.HasPrefix(b, []byte(mqttReplayReplyMagic)) {
		return ch.MQTTReplayPage{}, errMQTTReplayRPC
	}
	r := bytes.NewReader(b[len(mqttReplayReplyMagic):])
	echo, err := readMQTTSourceString(r, mqttReplayRPCMaxRequestBytes)
	if err != nil {
		return ch.MQTTReplayPage{}, errMQTTReplayRPC
	}
	actual, err := decodeMQTTReplayRequest([]byte(echo))
	if err != nil || actual != req {
		return ch.MQTTReplayPage{}, errMQTTReplayRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return ch.MQTTReplayPage{}, errMQTTReplayRPC
	}
	if status != 0 {
		if r.Len() != 0 {
			return ch.MQTTReplayPage{}, errMQTTReplayRPC
		}
		return ch.MQTTReplayPage{}, mqttSourceStatuses[status]
	}
	before, err := readMQTTReplayPrefix(r)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	after, err := readMQTTReplayPrefix(r)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	var count uint16
	if binary.Read(r, binary.BigEndian, &count) != nil || count == 0 || int(count) > req.Request.Range.Limit || int(count)*116 > r.Len() {
		return ch.MQTTReplayPage{}, errMQTTReplayRPC
	}
	page := ch.MQTTReplayPage{Before: before, After: after, Records: make([]ch.MQTTReplayRecord, int(count))}
	remaining := req.Request.Range.MaxBytes
	for i := range page.Records {
		var values [6]uint64
		if binary.Read(r, binary.BigEndian, &values) != nil {
			return ch.MQTTReplayPage{}, errMQTTReplayRPC
		}
		record := &page.Records[i]
		record.Position, record.ContentVersion, record.MessageID = values[0], values[1], values[2]
		record.AccountedBytes, record.TotalBytes, record.TotalStoredBytes = values[3], values[4], values[5]
		if _, err := io.ReadFull(r, record.ContentHash[:]); err != nil {
			return ch.MQTTReplayPage{}, errMQTTReplayRPC
		}
		if _, err := io.ReadFull(r, record.Digest[:]); err != nil {
			return ch.MQTTReplayPage{}, errMQTTReplayRPC
		}
		var size uint32
		if binary.Read(r, binary.BigEndian, &size) != nil || size == 0 || uint64(size) > uint64(remaining) || uint64(size) > uint64(r.Len()) {
			return ch.MQTTReplayPage{}, errMQTTReplayRPC
		}
		record.Content = make([]byte, int(size))
		if _, err := io.ReadFull(r, record.Content); err != nil {
			return ch.MQTTReplayPage{}, errMQTTReplayRPC
		}
		remaining -= int(size)
	}
	if r.Len() != 0 || !page.ValidFor(req.Request.Range) {
		return ch.MQTTReplayPage{}, errMQTTReplayRPC
	}
	return page, nil
}

// ForwardMQTTReplay requires the dedicated versioned codec and exact reply echo.
func (c *TransportClient) ForwardMQTTReplay(ctx context.Context, node ch.NodeID, req ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
	forward := mqttReplayForwardRequest{Leader: node, Request: req}
	b, err := encodeMQTTReplayRequest(forward)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTReplay, b)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	return decodeMQTTReplayReply(reply, forward)
}

func registerMQTTReplayHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTReplay(context.Context, mqttReplayForwardRequest) (ch.MQTTReplayPage, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTReplay, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		req, err := decodeMQTTReplayRequest(b)
		if err != nil {
			return nil, err
		}
		page, err := server.handleForwardMQTTReplay(ctx, req)
		return encodeMQTTReplayReply(req, page, err)
	}))
}
