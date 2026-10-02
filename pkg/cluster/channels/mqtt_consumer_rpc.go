package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const mqttConsumerRequestMagic = "WMCQ\x02"
const mqttConsumerReplyMagic = "WMCR\x02"
const mqttConsumerHeaderBytes = 13

// Freeze the nested message format independently of future ordinary RPC changes.
const mqttConsumerMessageCodecVersion uint8 = 11

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

// Consumer reply v2 uses the frozen publication-capable message codec rather
// than a storage envelope. The exact request echo includes the anchor and limits.
func encodeMQTTConsumerReadReply(q mqttConsumerReadForwardRequest, p ch.MQTTReplayConsumerPage, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTConsumerReadRequest(q)
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
	} else if !p.ValidFor(q.Request.Request.ChannelID, q.Request.Request.Range) {
		return nil, errMQTTConsumerRPC
	}
	b := binary.BigEndian.AppendUint64([]byte(mqttConsumerReplyMagic), q.Request.AnchorPosition)
	b = binary.BigEndian.AppendUint16(b, uint16(len(echo)))
	b = append(b, echo...)
	b = append(b, status)
	if status != 0 {
		return b, nil
	}
	b = appendMQTTReplayPrefix(b, p.Before)
	b = appendMQTTReplayPrefix(b, p.After)
	b = binary.BigEndian.AppendUint16(b, uint16(len(p.Records)))
	for _, entry := range p.Records {
		for _, v := range []uint64{entry.ContentVersion, entry.AccountedBytes, entry.TotalBytes, entry.TotalStoredBytes} {
			b = binary.BigEndian.AppendUint64(b, v)
		}
		b = append(b, entry.ContentHash[:]...)
		b = append(b, entry.Digest[:]...)
		b = appendBool(b, entry.Internal)
		b = appendMessage(b, entry.Message, mqttConsumerMessageCodecVersion)
	}
	if len(b) > mqttReplayRPCMaxReplyBytes {
		return nil, errMQTTConsumerRPC
	}
	return b, nil
}

func decodeMQTTConsumerReadReply(b []byte, q mqttConsumerReadForwardRequest) (ch.MQTTReplayConsumerPage, error) {
	var empty ch.MQTTReplayConsumerPage
	echo, err := encodeMQTTConsumerReadRequest(q)
	if err != nil {
		return empty, err
	}
	if len(b) < mqttConsumerHeaderBytes+2+len(echo)+1 || len(b) > mqttReplayRPCMaxReplyBytes || !bytes.HasPrefix(b, []byte(mqttConsumerReplyMagic)) ||
		binary.BigEndian.Uint64(b[len(mqttConsumerReplyMagic):]) != q.Request.AnchorPosition ||
		int(binary.BigEndian.Uint16(b[mqttConsumerHeaderBytes:])) != len(echo) {
		return empty, errMQTTConsumerRPC
	}
	offset := mqttConsumerHeaderBytes + 2
	if !bytes.Equal(b[offset:offset+len(echo)], echo) {
		return empty, errMQTTConsumerRPC
	}
	offset += len(echo)
	status := int(b[offset])
	offset++
	if status >= len(mqttSourceStatuses) {
		return empty, errMQTTConsumerRPC
	}
	if status != 0 {
		if offset != len(b) {
			return empty, errMQTTConsumerRPC
		}
		return empty, mqttSourceStatuses[status]
	}
	r := bytes.NewReader(b[offset:])
	before, err := readMQTTReplayPrefix(r)
	if err != nil {
		return empty, err
	}
	after, err := readMQTTReplayPrefix(r)
	if err != nil {
		return empty, err
	}
	var count uint16
	if binary.Read(r, binary.BigEndian, &count) != nil || count == 0 || int(count) > q.Request.Request.Range.Limit {
		return empty, errMQTTConsumerRPC
	}
	offset = len(b) - r.Len()
	page := ch.MQTTReplayConsumerPage{Before: before, After: after, Records: make([]ch.MQTTReplayPublication, 0, int(count))}
	for range count {
		if len(b)-offset < 97 {
			return empty, errMQTTConsumerRPC
		}
		data := b[offset:]
		entry := ch.MQTTReplayPublication{ContentVersion: binary.BigEndian.Uint64(data), AccountedBytes: binary.BigEndian.Uint64(data[8:]), TotalBytes: binary.BigEndian.Uint64(data[16:]), TotalStoredBytes: binary.BigEndian.Uint64(data[24:])}
		copy(entry.ContentHash[:], data[32:64])
		copy(entry.Digest[:], data[64:96])
		if data[96] > 1 {
			return empty, errMQTTConsumerRPC
		}
		entry.Internal = data[96] == 1
		entry.Message, offset, err = readMessage(b, offset+97, mqttConsumerMessageCodecVersion)
		if err != nil {
			return empty, err
		}
		page.Records = append(page.Records, entry)
	}
	if offset != len(b) || !page.ValidFor(q.Request.Request.ChannelID, q.Request.Request.Range) {
		return empty, errMQTTConsumerRPC
	}
	return page, nil
}

func (c *TransportClient) ForwardMQTTConsumerRead(ctx context.Context, node ch.NodeID, q ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error) {
	request := mqttConsumerReadForwardRequest{Leader: node, Request: q}
	b, err := encodeMQTTConsumerReadRequest(request)
	if err != nil {
		return ch.MQTTReplayConsumerPage{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTConsumerRead, b)
	if err != nil {
		return ch.MQTTReplayConsumerPage{}, err
	}
	return decodeMQTTConsumerReadReply(reply, request)
}
func registerMQTTConsumerReadHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTConsumerRead(context.Context, mqttConsumerReadForwardRequest) (ch.MQTTReplayConsumerPage, error)
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
