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

const willReceiptRPCMaxBytes = 70 << 10
const willReceiptRequestMagic = "WWRQ\x01"
const willReceiptReplyMagic = "WWRR\x01"

var errWillReceiptRPC = errors.New("channels: invalid Will receipt RPC")

func encodeWillReceiptRequest(q willReceiptForwardRequest) ([]byte, error) {
	if q.Leader == 0 || !q.Request.Valid() {
		return nil, errWillReceiptRPC
	}
	b := []byte(willReceiptRequestMagic)
	for _, v := range []uint64{uint64(q.Leader), q.Request.ExpectedChannelEpoch, q.Request.ExpectedLeaderEpoch, q.Request.ExpectedRouteGeneration} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	b = append(b, q.Request.ChannelID.Type)
	for _, s := range []string{q.Request.ChannelID.ID, q.Request.FromUID, q.Request.ServerWillKey} {
		b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
		b = append(b, s...)
	}
	return b, nil
}
func decodeWillReceiptRequest(b []byte) (willReceiptForwardRequest, error) {
	var empty willReceiptForwardRequest
	if len(b) > willReceiptRPCMaxBytes || !bytes.HasPrefix(b, []byte(willReceiptRequestMagic)) {
		return empty, errWillReceiptRPC
	}
	r := bytes.NewReader(b[len(willReceiptRequestMagic):])
	var fields [4]uint64
	if binary.Read(r, binary.BigEndian, &fields) != nil {
		return empty, errWillReceiptRPC
	}
	typ, err := r.ReadByte()
	if err != nil {
		return empty, errWillReceiptRPC
	}
	values := make([]string, 3)
	for i, max := range []int{1024, 65535, 128} {
		values[i], err = readMQTTSourceString(r, max)
		if err != nil {
			return empty, errWillReceiptRPC
		}
	}
	q := willReceiptForwardRequest{Leader: ch.NodeID(fields[0]), Request: ch.WillReceiptRequest{ChannelID: ch.ChannelID{ID: values[0], Type: typ}, ExpectedChannelEpoch: fields[1], ExpectedLeaderEpoch: fields[2], ExpectedRouteGeneration: fields[3], FromUID: values[1], ServerWillKey: values[2]}}
	if r.Len() != 0 || q.Leader == 0 || !q.Request.Valid() {
		return empty, errWillReceiptRPC
	}
	return q, nil
}

// Replies echo every identity/fence and use a fixed proof body even for absence.
// The uint32 echo size preserves the complete storage UID domain within 70 KiB.
func encodeWillReceiptReply(q willReceiptForwardRequest, p ch.WillReceiptResult, operationErr error) ([]byte, error) {
	echo, err := encodeWillReceiptRequest(q)
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
		p = ch.WillReceiptResult{}
	} else if !p.Valid() {
		return nil, errWillReceiptRPC
	}
	b := binary.BigEndian.AppendUint32([]byte(willReceiptReplyMagic), uint32(len(echo)))
	b = append(b, echo...)
	b = append(b, status)
	b = binary.BigEndian.AppendUint64(b, p.CommittedThrough)
	if p.Found {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	for _, v := range []uint64{p.Receipt.MessageID, p.Receipt.MessageSeq, uint64(p.Receipt.ServerTimestampMS)} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	b = append(b, p.Receipt.ContentHash[:]...)
	return b, nil
}
func decodeWillReceiptReply(b []byte, q willReceiptForwardRequest) (ch.WillReceiptResult, error) {
	var empty ch.WillReceiptResult
	if len(b) > willReceiptRPCMaxBytes || !bytes.HasPrefix(b, []byte(willReceiptReplyMagic)) {
		return empty, errWillReceiptRPC
	}
	r := bytes.NewReader(b[len(willReceiptReplyMagic):])
	var size uint32
	if binary.Read(r, binary.BigEndian, &size) != nil || uint64(size) > uint64(r.Len()) {
		return empty, errWillReceiptRPC
	}
	echo := make([]byte, int(size))
	if _, err := io.ReadFull(r, echo); err != nil {
		return empty, errWillReceiptRPC
	}
	actual, err := decodeWillReceiptRequest(echo)
	if err != nil || actual != q {
		return empty, errWillReceiptRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return empty, errWillReceiptRPC
	}
	var p ch.WillReceiptResult
	if binary.Read(r, binary.BigEndian, &p.CommittedThrough) != nil {
		return empty, errWillReceiptRPC
	}
	found, err := r.ReadByte()
	if err != nil || found > 1 {
		return empty, errWillReceiptRPC
	}
	p.Found = found == 1
	var fields [3]uint64
	if binary.Read(r, binary.BigEndian, &fields) != nil {
		return empty, errWillReceiptRPC
	}
	p.Receipt = ch.WillReceipt{MessageID: fields[0], MessageSeq: fields[1], ServerTimestampMS: int64(fields[2])}
	if _, err = io.ReadFull(r, p.Receipt.ContentHash[:]); err != nil || r.Len() != 0 || !p.Valid() {
		return empty, errWillReceiptRPC
	}
	if status != 0 {
		if p != empty {
			return empty, errWillReceiptRPC
		}
		return empty, mqttSourceStatuses[status]
	}
	return p, nil
}
func (c *TransportClient) ForwardWillReceipt(ctx context.Context, node ch.NodeID, q ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
	request := willReceiptForwardRequest{Leader: node, Request: q}
	b, err := encodeWillReceiptRequest(request)
	if err != nil {
		return ch.WillReceiptResult{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelWillReceipt, b)
	if err != nil {
		return ch.WillReceiptResult{}, err
	}
	return decodeWillReceiptReply(reply, request)
}
func registerWillReceiptHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardWillReceipt(context.Context, willReceiptForwardRequest) (ch.WillReceiptResult, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelWillReceipt, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeWillReceiptRequest(b)
		if err != nil {
			return nil, err
		}
		p, err := server.handleForwardWillReceipt(ctx, q)
		return encodeWillReceiptReply(q, p, err)
	}))
}
