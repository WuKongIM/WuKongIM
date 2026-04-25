package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
)

const (
	// Keep ISR node transport services out of the shared cluster RPC range.
	RPCServiceFetch          uint8 = 30
	RPCServiceLongPollFetch  uint8 = 35
	RPCServiceReconcileProbe uint8 = 34

	fetchRequestCodecVersion  byte = 1
	fetchResponseCodecVersion byte = 1
	longPollRequestCodecVer   byte = 1
	longPollResponseCodecVer  byte = 1
	reconcileProbeCodecVer    byte = 1
	reconcileProbeRespVer     byte = 1
)

func encodeFetchRequest(req runtime.FetchRequestEnvelope) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 80))
	buf.WriteByte(fetchRequestCodecVersion)
	if err := writeChannelKey(buf, req.ChannelKey); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.Epoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.Generation); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint64(req.ReplicaID)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.FetchOffset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.OffsetEpoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, int64(req.MaxBytes)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeFetchRequest(data []byte) (runtime.FetchRequestEnvelope, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	if version != fetchRequestCodecVersion {
		return runtime.FetchRequestEnvelope{}, fmt.Errorf("channeltransport: unknown fetch request codec version %d", version)
	}

	channelKey, err := readChannelKey(rd)
	if err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	var req runtime.FetchRequestEnvelope
	req.ChannelKey = channelKey
	if err := binary.Read(rd, binary.BigEndian, &req.Epoch); err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.Generation); err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	var replicaID uint64
	if err := binary.Read(rd, binary.BigEndian, &replicaID); err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	req.ReplicaID = channel.NodeID(replicaID)
	if err := binary.Read(rd, binary.BigEndian, &req.FetchOffset); err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.OffsetEpoch); err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	var maxBytes int64
	if err := binary.Read(rd, binary.BigEndian, &maxBytes); err != nil {
		return runtime.FetchRequestEnvelope{}, err
	}
	req.MaxBytes = int(maxBytes)
	if rd.Len() != 0 {
		return runtime.FetchRequestEnvelope{}, fmt.Errorf("channeltransport: trailing fetch request payload bytes")
	}
	return req, nil
}

func encodeFetchResponse(resp runtime.FetchResponseEnvelope) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 96))
	buf.WriteByte(fetchResponseCodecVersion)
	if err := writeChannelKey(buf, resp.ChannelKey); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.Epoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.Generation); err != nil {
		return nil, err
	}
	if resp.TruncateTo != nil {
		buf.WriteByte(1)
		if err := binary.Write(buf, binary.BigEndian, *resp.TruncateTo); err != nil {
			return nil, err
		}
	} else {
		buf.WriteByte(0)
	}
	if err := binary.Write(buf, binary.BigEndian, resp.LeaderHW); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(resp.Records))); err != nil {
		return nil, err
	}
	for _, record := range resp.Records {
		if err := binary.Write(buf, binary.BigEndian, int64(record.SizeBytes)); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, uint32(len(record.Payload))); err != nil {
			return nil, err
		}
		if _, err := buf.Write(record.Payload); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeFetchResponse(data []byte) (runtime.FetchResponseEnvelope, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	if version != fetchResponseCodecVersion {
		return runtime.FetchResponseEnvelope{}, fmt.Errorf("channeltransport: unknown fetch response codec version %d", version)
	}

	channelKey, err := readChannelKey(rd)
	if err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	resp := runtime.FetchResponseEnvelope{ChannelKey: channelKey}
	if err := binary.Read(rd, binary.BigEndian, &resp.Epoch); err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.Generation); err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	var truncateFlag byte
	if err := binary.Read(rd, binary.BigEndian, &truncateFlag); err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	if truncateFlag == 1 {
		var truncateTo uint64
		if err := binary.Read(rd, binary.BigEndian, &truncateTo); err != nil {
			return runtime.FetchResponseEnvelope{}, err
		}
		resp.TruncateTo = &truncateTo
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.LeaderHW); err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	var count uint32
	if err := binary.Read(rd, binary.BigEndian, &count); err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	resp.Records = make([]channel.Record, 0, count)
	for i := uint32(0); i < count; i++ {
		var sizeBytes int64
		if err := binary.Read(rd, binary.BigEndian, &sizeBytes); err != nil {
			return runtime.FetchResponseEnvelope{}, err
		}
		var payloadLen uint32
		if err := binary.Read(rd, binary.BigEndian, &payloadLen); err != nil {
			return runtime.FetchResponseEnvelope{}, err
		}
		recordPayload := make([]byte, payloadLen)
		if _, err := io.ReadFull(rd, recordPayload); err != nil {
			return runtime.FetchResponseEnvelope{}, err
		}
		resp.Records = append(resp.Records, channel.Record{
			Payload:   recordPayload,
			SizeBytes: int(sizeBytes),
		})
	}
	if rd.Len() != 0 {
		return runtime.FetchResponseEnvelope{}, fmt.Errorf("channeltransport: trailing fetch response payload bytes")
	}
	return resp, nil
}

func encodeReconcileProbeRequest(req runtime.ReconcileProbeRequestEnvelope) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 48))
	buf.WriteByte(reconcileProbeCodecVer)
	if err := writeChannelKey(buf, req.ChannelKey); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.Epoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.Generation); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint64(req.ReplicaID)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeReconcileProbeRequest(data []byte) (runtime.ReconcileProbeRequestEnvelope, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return runtime.ReconcileProbeRequestEnvelope{}, err
	}
	if version != reconcileProbeCodecVer {
		return runtime.ReconcileProbeRequestEnvelope{}, fmt.Errorf("channeltransport: unknown reconcile probe codec version %d", version)
	}

	channelKey, err := readChannelKey(rd)
	if err != nil {
		return runtime.ReconcileProbeRequestEnvelope{}, err
	}
	req := runtime.ReconcileProbeRequestEnvelope{ChannelKey: channelKey}
	if err := binary.Read(rd, binary.BigEndian, &req.Epoch); err != nil {
		return runtime.ReconcileProbeRequestEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.Generation); err != nil {
		return runtime.ReconcileProbeRequestEnvelope{}, err
	}
	var replicaID uint64
	if err := binary.Read(rd, binary.BigEndian, &replicaID); err != nil {
		return runtime.ReconcileProbeRequestEnvelope{}, err
	}
	req.ReplicaID = channel.NodeID(replicaID)
	if rd.Len() != 0 {
		return runtime.ReconcileProbeRequestEnvelope{}, fmt.Errorf("channeltransport: trailing reconcile probe payload bytes")
	}
	return req, nil
}

func encodeReconcileProbeResponse(resp runtime.ReconcileProbeResponseEnvelope) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 64))
	buf.WriteByte(reconcileProbeRespVer)
	if err := writeChannelKey(buf, resp.ChannelKey); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.Epoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.Generation); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint64(resp.ReplicaID)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.OffsetEpoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.LogEndOffset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.CheckpointHW); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeReconcileProbeResponse(data []byte) (runtime.ReconcileProbeResponseEnvelope, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if version != reconcileProbeRespVer {
		return runtime.ReconcileProbeResponseEnvelope{}, fmt.Errorf("channeltransport: unknown reconcile probe response codec version %d", version)
	}

	channelKey, err := readChannelKey(rd)
	if err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	resp := runtime.ReconcileProbeResponseEnvelope{ChannelKey: channelKey}
	if err := binary.Read(rd, binary.BigEndian, &resp.Epoch); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.Generation); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	var replicaID uint64
	if err := binary.Read(rd, binary.BigEndian, &replicaID); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	resp.ReplicaID = channel.NodeID(replicaID)
	if err := binary.Read(rd, binary.BigEndian, &resp.OffsetEpoch); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.LogEndOffset); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.CheckpointHW); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if rd.Len() != 0 {
		return runtime.ReconcileProbeResponseEnvelope{}, fmt.Errorf("channeltransport: trailing reconcile probe response payload bytes")
	}
	return resp, nil
}

func writeChannelKey(buf *bytes.Buffer, channelKey channel.ChannelKey) error {
	if err := binary.Write(buf, binary.BigEndian, uint32(len(channelKey))); err != nil {
		return err
	}
	if _, err := buf.WriteString(string(channelKey)); err != nil {
		return err
	}
	return nil
}

func readChannelKey(rd *bytes.Reader) (channel.ChannelKey, error) {
	var length uint32
	if err := binary.Read(rd, binary.BigEndian, &length); err != nil {
		return "", err
	}
	channelKey := make([]byte, length)
	if _, err := io.ReadFull(rd, channelKey); err != nil {
		return "", err
	}
	return channel.ChannelKey(channelKey), nil
}
