package channels

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

const mqttAnchorRPCMaxBytes = 8 << 10
const mqttAnchorRequestMagic = "WMAQ\x01"
const mqttAnchorReplyMagic = "WMAR\x01"

var errMQTTAnchorRPC = errors.New("channels: invalid MQTT anchor RPC")

func encodeMQTTAnchorRequest(q mqttAnchorForwardRequest) ([]byte, error) {
	if !q.valid() {
		return nil, errMQTTAnchorRPC
	}
	r := q.Copy
	copyBody, err := encodeMQTTCopyRequest(mqttCopyRequest{Target: r.Leader, Leader: r.Leader, Request: r.Request, Authority: r.Authority, Before: r.Before, After: r.After})
	if err != nil {
		return nil, errMQTTAnchorRPC
	}
	b := appendMQTTReplayString([]byte(mqttAnchorRequestMagic), string(copyBody))
	b = binary.BigEndian.AppendUint64(b, q.MessageID)
	b = binary.BigEndian.AppendUint64(b, uint64(q.ServerTimestampMS))
	b = binary.BigEndian.AppendUint16(b, uint16(r.WriteQuorum))
	b = binary.BigEndian.AppendUint16(b, uint16(len(r.Copies)))
	for _, n := range r.Copies {
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}
	if len(b) > mqttAnchorRPCMaxBytes {
		return nil, errMQTTAnchorRPC
	}
	return b, nil
}

func decodeMQTTAnchorRequest(b []byte) (mqttAnchorForwardRequest, error) {
	var empty mqttAnchorForwardRequest
	if len(b) > mqttAnchorRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttAnchorRequestMagic)) {
		return empty, errMQTTAnchorRPC
	}
	r := bytes.NewReader(b[len(mqttAnchorRequestMagic):])
	body, err := readMQTTSourceString(r, mqttCopyRPCMaxBytes)
	if err != nil {
		return empty, errMQTTAnchorRPC
	}
	c, err := decodeMQTTCopyRequest([]byte(body))
	if err != nil || c.Target != c.Leader {
		return empty, errMQTTAnchorRPC
	}
	var values [2]uint64
	var counts [2]uint16
	if binary.Read(r, binary.BigEndian, &values) != nil || binary.Read(r, binary.BigEndian, &counts) != nil || counts[0] == 0 || counts[0] > 256 || counts[1] < counts[0] || counts[1] > 256 || r.Len() != int(counts[1])*8 {
		return empty, errMQTTAnchorRPC
	}
	q := mqttAnchorForwardRequest{MessageID: values[0], ServerTimestampMS: int64(values[1]), Copy: ch.MQTTReplayCopyReceipt{Request: c.Request, Leader: c.Leader, Authority: c.Authority, Before: c.Before, After: c.After, WriteQuorum: int(counts[0]), Copies: make([]ch.NodeID, int(counts[1]))}}
	for i := range q.Copy.Copies {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil {
			return empty, errMQTTAnchorRPC
		}
		q.Copy.Copies[i] = ch.NodeID(n)
	}
	if !q.valid() {
		return empty, errMQTTAnchorRPC
	}
	return q, nil
}

func encodeMQTTAnchorReply(q mqttAnchorForwardRequest, p ch.MQTTReplayAnchorProof, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTAnchorRequest(q)
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
	} else if !validMQTTAnchorProof(q, p) {
		return nil, errMQTTAnchorRPC
	}
	b := appendMQTTReplayString([]byte(mqttAnchorReplyMagic), string(echo))
	b = append(b, status)
	if operationErr == nil {
		b, err = appendMQTTAnchorProof(b, p)
		if err != nil {
			return nil, errMQTTAnchorRPC
		}
	}
	if len(b) > mqttAnchorRPCMaxBytes {
		return nil, errMQTTAnchorRPC
	}
	return b, nil
}

func decodeMQTTAnchorReply(b []byte, q mqttAnchorForwardRequest) (ch.MQTTReplayAnchorProof, error) {
	var p ch.MQTTReplayAnchorProof
	if len(b) > mqttAnchorRPCMaxBytes || !bytes.HasPrefix(b, []byte(mqttAnchorReplyMagic)) {
		return p, errMQTTAnchorRPC
	}
	r := bytes.NewReader(b[len(mqttAnchorReplyMagic):])
	echo, err := readMQTTSourceString(r, mqttAnchorRPCMaxBytes)
	if err != nil {
		return p, errMQTTAnchorRPC
	}
	want, err := encodeMQTTAnchorRequest(q)
	if err != nil || !bytes.Equal([]byte(echo), want) {
		return p, errMQTTAnchorRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return p, errMQTTAnchorRPC
	}
	if status != 0 {
		if r.Len() != 0 {
			return p, errMQTTAnchorRPC
		}
		return p, mqttSourceStatuses[status]
	}
	p, err = readMQTTAnchorProof(r)
	if err != nil || r.Len() != 0 || !validMQTTAnchorProof(q, p) {
		return ch.MQTTReplayAnchorProof{}, errMQTTAnchorRPC
	}
	return p, nil
}

// appendMQTTAnchorProof is the fixed version-1 proof envelope shared by anchor
// commit and planning replies. The enclosing request binds its source/authority.
func appendMQTTAnchorProof(b []byte, p ch.MQTTReplayAnchorProof) ([]byte, error) {
	anchor, err := p.Anchor.MarshalBinary()
	if err != nil {
		return nil, errMQTTAnchorRPC
	}
	b = appendMQTTReplayString(b, string(anchor))
	m := p.Manifest
	b = binary.BigEndian.AppendUint16(b, m.Version)
	for _, v := range []uint64{m.ChannelEpoch, m.LeaderTerm, m.FenceVersion, m.BaseOffset, m.LastOffset, m.PreviousTerm, m.PreviousIndex} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	b = append(b, m.CommandID[:]...)
	b = append(b, m.PreviousDigest[:]...)
	return append(b, m.Digest[:]...), nil
}

func readMQTTAnchorProof(r *bytes.Reader) (ch.MQTTReplayAnchorProof, error) {
	var p ch.MQTTReplayAnchorProof
	body, err := readMQTTSourceString(r, 256)
	if err != nil {
		return p, errMQTTAnchorRPC
	}
	p.Anchor, err = quorumlog.DecodeMQTTReplayAnchor([]byte(body))
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, errMQTTAnchorRPC
	}
	var version uint16
	var v [7]uint64
	if binary.Read(r, binary.BigEndian, &version) != nil || binary.Read(r, binary.BigEndian, &v) != nil {
		return ch.MQTTReplayAnchorProof{}, errMQTTAnchorRPC
	}
	p.Manifest = ch.ProposalManifest{Version: version, ChannelEpoch: v[0], LeaderTerm: v[1], FenceVersion: v[2], BaseOffset: v[3], LastOffset: v[4], PreviousTerm: v[5], PreviousIndex: v[6]}
	for _, dst := range [][]byte{p.Manifest.CommandID[:], p.Manifest.PreviousDigest[:], p.Manifest.Digest[:]} {
		if _, err := io.ReadFull(r, dst); err != nil {
			return ch.MQTTReplayAnchorProof{}, errMQTTAnchorRPC
		}
	}
	return p, nil
}

// ForwardMQTTReplayAnchor binds the evidence to one exact serving node and the
// closed codec; old peers fail explicitly without a compatibility fallback.
func (c *TransportClient) ForwardMQTTReplayAnchor(ctx context.Context, node ch.NodeID, q mqttAnchorForwardRequest) (ch.MQTTReplayAnchorProof, error) {
	if node != q.Copy.Leader {
		return ch.MQTTReplayAnchorProof{}, errMQTTAnchorRPC
	}
	b, err := encodeMQTTAnchorRequest(q)
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTAnchor, b)
	if err != nil {
		return ch.MQTTReplayAnchorProof{}, err
	}
	return decodeMQTTAnchorReply(reply, q)
}

func registerMQTTAnchorHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleForwardMQTTReplayAnchor(context.Context, mqttAnchorForwardRequest) (ch.MQTTReplayAnchorProof, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTAnchor, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTAnchorRequest(b)
		if err != nil {
			return nil, err
		}
		p, err := server.handleForwardMQTTReplayAnchor(ctx, q)
		return encodeMQTTAnchorReply(q, p, err)
	}))
}
