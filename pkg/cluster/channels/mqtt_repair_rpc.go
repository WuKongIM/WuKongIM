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
	mqttRepairRequestMagic       = "WMDQ\x01"
	mqttRepairReplyMagic         = "WMDR\x01"
	mqttRepairRPCMaxRequestBytes = 4096
	mqttRepairRPCMaxReplyBytes   = mqttReplayRPCMaxReplyBytes + 4096
)

var errMQTTRepairRPC = errors.New("channels: invalid MQTT repair RPC")

type mqttRepairRPCRequest struct {
	Export  bool
	Request ch.MQTTReplayRepairRequest
}
type mqttRepairRPCResult struct {
	Prefix ch.MQTTReplayPrefix
	Page   ch.MQTTReplayPage
}

func encodeMQTTRepairRequest(q mqttRepairRPCRequest) ([]byte, error) {
	if !q.Request.Valid() {
		return nil, errMQTTRepairRPC
	}
	r := q.Request
	inner, err := encodeMQTTReplayRequest(mqttReplayForwardRequest{Leader: r.Donor, Request: r.Request})
	if err != nil {
		return nil, errMQTTRepairRPC
	}
	action := byte(1)
	if q.Export {
		action = 2
	}
	b := append([]byte(mqttRepairRequestMagic), action)
	for _, v := range []uint64{uint64(r.Target), uint64(r.Donor), r.AnchorPosition} {
		b = binary.BigEndian.AppendUint64(b, v)
	}
	b = appendMQTTReplayString(b, string(inner))
	if len(b) > mqttRepairRPCMaxRequestBytes {
		return nil, errMQTTRepairRPC
	}
	return b, nil
}

func decodeMQTTRepairRequest(b []byte) (mqttRepairRPCRequest, error) {
	var empty mqttRepairRPCRequest
	if len(b) > mqttRepairRPCMaxRequestBytes || !bytes.HasPrefix(b, []byte(mqttRepairRequestMagic)) {
		return empty, errMQTTRepairRPC
	}
	r := bytes.NewReader(b[len(mqttRepairRequestMagic):])
	action, err := r.ReadByte()
	if err != nil || (action != 1 && action != 2) {
		return empty, errMQTTRepairRPC
	}
	var fields [3]uint64
	if binary.Read(r, binary.BigEndian, &fields) != nil {
		return empty, errMQTTRepairRPC
	}
	nested, err := readMQTTSourceString(r, mqttRepairRPCMaxRequestBytes)
	if err != nil || r.Len() != 0 {
		return empty, errMQTTRepairRPC
	}
	inner, err := decodeMQTTReplayRequest([]byte(nested))
	if err != nil || uint64(inner.Leader) != fields[1] {
		return empty, errMQTTRepairRPC
	}
	q := mqttRepairRPCRequest{Export: action == 2, Request: ch.MQTTReplayRepairRequest{Target: ch.NodeID(fields[0]), Donor: ch.NodeID(fields[1]), AnchorPosition: fields[2], Request: inner.Request}}
	if !q.Request.Valid() {
		return empty, errMQTTRepairRPC
	}
	return q, nil
}

func encodeMQTTRepairReply(q mqttRepairRPCRequest, p mqttRepairRPCResult, operationErr error) ([]byte, error) {
	echo, err := encodeMQTTRepairRequest(q)
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
	}
	b := appendMQTTReplayString([]byte(mqttRepairReplyMagic), string(echo))
	b = append(b, status)
	if status != 0 {
		return b, nil
	}
	if q.Export {
		if p.Prefix != (ch.MQTTReplayPrefix{}) || !q.Request.AcceptsPrefix(p.Page.After) {
			return nil, errMQTTRepairRPC
		}
		// Keep the established replay-page codec byte-for-byte; the outer exact echo
		// additionally binds the action, target and independently selected anchor.
		nested, err := encodeMQTTReplayReply(mqttReplayForwardRequest{Leader: q.Request.Donor, Request: q.Request.Request}, p.Page, nil)
		if err != nil {
			return nil, errMQTTRepairRPC
		}
		b = binary.BigEndian.AppendUint32(b, uint32(len(nested)))
		b = append(b, nested...)
	} else {
		if len(p.Page.Records) != 0 || p.Page.Before != (ch.MQTTReplayPrefix{}) || p.Page.After != (ch.MQTTReplayPrefix{}) || !q.Request.AcceptsPrefix(p.Prefix) {
			return nil, errMQTTRepairRPC
		}
		b = appendMQTTReplayPrefix(b, p.Prefix)
	}
	if len(b) > mqttRepairRPCMaxReplyBytes {
		return nil, errMQTTRepairRPC
	}
	return b, nil
}

func decodeMQTTRepairReply(b []byte, q mqttRepairRPCRequest) (mqttRepairRPCResult, error) {
	var empty mqttRepairRPCResult
	if len(b) > mqttRepairRPCMaxReplyBytes || !bytes.HasPrefix(b, []byte(mqttRepairReplyMagic)) {
		return empty, errMQTTRepairRPC
	}
	r := bytes.NewReader(b[len(mqttRepairReplyMagic):])
	echo, err := readMQTTSourceString(r, mqttRepairRPCMaxRequestBytes)
	if err != nil {
		return empty, errMQTTRepairRPC
	}
	want, err := encodeMQTTRepairRequest(q)
	if err != nil || !bytes.Equal([]byte(echo), want) {
		return empty, errMQTTRepairRPC
	}
	status, err := r.ReadByte()
	if err != nil || int(status) >= len(mqttSourceStatuses) {
		return empty, errMQTTRepairRPC
	}
	if status != 0 {
		if r.Len() != 0 {
			return empty, errMQTTRepairRPC
		}
		return empty, mqttSourceStatuses[status]
	}
	var out mqttRepairRPCResult
	if q.Export {
		var size uint32
		if binary.Read(r, binary.BigEndian, &size) != nil || uint64(size) != uint64(r.Len()) || size > mqttReplayRPCMaxReplyBytes {
			return empty, errMQTTRepairRPC
		}
		// A borrowed slice avoids duplicating a full page before its decoder creates
		// independent record bytes. The transport buffer need not outlive this call.
		nested := b[len(b)-r.Len():]
		out.Page, err = decodeMQTTReplayReply(nested, mqttReplayForwardRequest{Leader: q.Request.Donor, Request: q.Request.Request})
		if err != nil || !q.Request.AcceptsPrefix(out.Page.After) {
			return empty, errMQTTRepairRPC
		}
		_, _ = r.Seek(0, io.SeekEnd)
	} else {
		out.Prefix, err = readMQTTReplayPrefix(r)
		if err != nil || !q.Request.AcceptsPrefix(out.Prefix) {
			return empty, errMQTTRepairRPC
		}
	}
	if r.Len() != 0 {
		return empty, errMQTTRepairRPC
	}
	return out, nil
}

func (c *TransportClient) ForwardMQTTReplayRepair(ctx context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPrefix, error) {
	result, err := c.callMQTTRepair(ctx, mqttRepairRPCRequest{Request: q}, q.Target)
	return result.Prefix, err
}
func (c *TransportClient) FetchMQTTReplayRepair(ctx context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
	result, err := c.callMQTTRepair(ctx, mqttRepairRPCRequest{Export: true, Request: q}, q.Donor)
	return result.Page, err
}
func (c *TransportClient) callMQTTRepair(ctx context.Context, q mqttRepairRPCRequest, node ch.NodeID) (mqttRepairRPCResult, error) {
	b, err := encodeMQTTRepairRequest(q)
	if err != nil {
		return mqttRepairRPCResult{}, err
	}
	reply, err := c.call(ctx, uint64(node), clusternet.RPCChannelMQTTRepair, b)
	if err != nil {
		return mqttRepairRPCResult{}, err
	}
	return decodeMQTTRepairReply(reply, q)
}
func registerMQTTRepairHandler(registrar HandlerRegistrar, service serviceRPCServer) {
	server, ok := service.(interface {
		handleMQTTReplayRepair(context.Context, mqttRepairRPCRequest) (mqttRepairRPCResult, error)
	})
	if !ok {
		return
	}
	registrar.Register(clusternet.RPCChannelMQTTRepair, clusternet.HandlerFunc(func(ctx context.Context, b []byte) ([]byte, error) {
		q, err := decodeMQTTRepairRequest(b)
		if err != nil {
			return nil, err
		}
		result, err := server.handleMQTTReplayRepair(ctx, q)
		return encodeMQTTRepairReply(q, result, err)
	}))
}
