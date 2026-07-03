package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel/runtime"
)

const (
	// Keep ISR node transport services out of the shared cluster RPC range.
	RPCServiceFetch          uint8 = 30
	RPCServiceLongPollFetch  uint8 = 35
	RPCServiceReconcileProbe uint8 = 34
	RPCServiceFenceAndDrain  uint8 = 48

	fetchRequestCodecVersion  byte = 1
	fetchResponseCodecVersion byte = 3
	longPollRequestCodecVer   byte = 2
	longPollResponseCodecVer  byte = 4
	reconcileProbeCodecVerV2  byte = 2
	reconcileProbeCodecVer    byte = 3
	reconcileProbeRespVerV2   byte = 2
	reconcileProbeRespVer     byte = 3
	fenceAndDrainReqVer       byte = 1
	fenceAndDrainRespVer      byte = 1
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
	if err := writeRetentionReset(buf, resp.RetentionReset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.LeaderHW); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(resp.Records))); err != nil {
		return nil, err
	}
	for _, record := range resp.Records {
		if err := writeRecord(buf, record); err != nil {
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
	reset, err := readRetentionReset(rd)
	if err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	resp.RetentionReset = reset
	if err := binary.Read(rd, binary.BigEndian, &resp.LeaderHW); err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	var count uint32
	if err := binary.Read(rd, binary.BigEndian, &count); err != nil {
		return runtime.FetchResponseEnvelope{}, err
	}
	resp.Records = make([]channel.Record, 0, count)
	for i := uint32(0); i < count; i++ {
		record, err := readRecord(rd)
		if err != nil {
			return runtime.FetchResponseEnvelope{}, err
		}
		resp.Records = append(resp.Records, record)
	}
	if rd.Len() != 0 {
		return runtime.FetchResponseEnvelope{}, fmt.Errorf("channeltransport: trailing fetch response payload bytes")
	}
	return resp, nil
}

func writeRecord(buf *bytes.Buffer, record channel.Record) error {
	buf.Grow(recordEncodedSize(record))
	var header [36]byte
	binary.BigEndian.PutUint64(header[0:8], record.ID)
	binary.BigEndian.PutUint64(header[8:16], record.Index)
	binary.BigEndian.PutUint64(header[16:24], record.Epoch)
	binary.BigEndian.PutUint64(header[24:32], uint64(record.SizeBytes))
	binary.BigEndian.PutUint32(header[32:36], uint32(len(record.Payload)))
	if _, err := buf.Write(header[:]); err != nil {
		return err
	}
	_, err := buf.Write(record.Payload)
	return err
}

func recordEncodedSize(record channel.Record) int {
	return 36 + len(record.Payload)
}

func readRecord(rd *bytes.Reader) (channel.Record, error) {
	var record channel.Record
	var header [36]byte
	if err := readFullBytesReader(rd, header[:]); err != nil {
		return channel.Record{}, err
	}
	record.ID = binary.BigEndian.Uint64(header[0:8])
	record.Index = binary.BigEndian.Uint64(header[8:16])
	record.Epoch = binary.BigEndian.Uint64(header[16:24])
	sizeBytes := int64(binary.BigEndian.Uint64(header[24:32]))
	payloadLen := binary.BigEndian.Uint32(header[32:36])
	record.Payload = make([]byte, payloadLen)
	if err := readFullBytesReader(rd, record.Payload); err != nil {
		return channel.Record{}, err
	}
	record.SizeBytes = int(sizeBytes)
	return record, nil
}

func readFullBytesReader(rd *bytes.Reader, dst []byte) error {
	if len(dst) == 0 {
		return nil
	}
	n, err := rd.Read(dst)
	if n == len(dst) {
		return nil
	}
	if err != nil && n == 0 {
		return err
	}
	return io.ErrUnexpectedEOF
}

func writeRetentionReset(buf *bytes.Buffer, reset *channel.RetentionReset) error {
	if reset == nil {
		return buf.WriteByte(0)
	}
	if err := buf.WriteByte(1); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.BigEndian, reset.RetentionThroughSeq); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.BigEndian, reset.RetainedThroughOffset); err != nil {
		return err
	}
	return binary.Write(buf, binary.BigEndian, reset.MinAvailableSeq)
}

func readRetentionReset(rd *bytes.Reader) (*channel.RetentionReset, error) {
	hasReset, err := rd.ReadByte()
	if err != nil {
		return nil, err
	}
	if hasReset == 0 {
		return nil, nil
	}
	reset := &channel.RetentionReset{}
	if err := binary.Read(rd, binary.BigEndian, &reset.RetentionThroughSeq); err != nil {
		return nil, err
	}
	if err := binary.Read(rd, binary.BigEndian, &reset.RetainedThroughOffset); err != nil {
		return nil, err
	}
	if err := binary.Read(rd, binary.BigEndian, &reset.MinAvailableSeq); err != nil {
		return nil, err
	}
	return reset, nil
}

func encodeReconcileProbeRequest(req runtime.ReconcileProbeRequestEnvelope) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 48))
	version := reconcileProbeCodecVerV2
	if req.RequireExtendedResponse {
		version = reconcileProbeCodecVer
	}
	buf.WriteByte(version)
	if err := writeChannelKey(buf, req.ChannelKey); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.Epoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.LeaderEpoch); err != nil {
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
	if version != reconcileProbeCodecVerV2 && version != reconcileProbeCodecVer {
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
	if err := binary.Read(rd, binary.BigEndian, &req.LeaderEpoch); err != nil {
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
	req.RequireExtendedResponse = version == reconcileProbeCodecVer
	if rd.Len() != 0 {
		return runtime.ReconcileProbeRequestEnvelope{}, fmt.Errorf("channeltransport: trailing reconcile probe payload bytes")
	}
	return req, nil
}

func encodeReconcileProbeResponse(resp runtime.ReconcileProbeResponseEnvelope) ([]byte, error) {
	return encodeReconcileProbeResponseVersion(resp, reconcileProbeRespVer)
}

func encodeReconcileProbeResponseForRequest(resp runtime.ReconcileProbeResponseEnvelope, req runtime.ReconcileProbeRequestEnvelope) ([]byte, error) {
	if req.RequireExtendedResponse {
		return encodeReconcileProbeResponseVersion(resp, reconcileProbeRespVer)
	}
	return encodeReconcileProbeResponseVersion(resp, reconcileProbeRespVerV2)
}

func encodeReconcileProbeResponseVersion(resp runtime.ReconcileProbeResponseEnvelope, version byte) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 96))
	buf.WriteByte(version)
	if err := writeChannelKey(buf, resp.ChannelKey); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.Epoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.LeaderEpoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.Generation); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint64(resp.ReplicaID)); err != nil {
		return nil, err
	}
	if version == reconcileProbeRespVerV2 {
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
	if version != reconcileProbeRespVer {
		return nil, fmt.Errorf("channeltransport: unknown reconcile probe response codec version %d", version)
	}
	if err := binary.Write(buf, binary.BigEndian, uint64(resp.Leader)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint64(resp.Role)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.OffsetEpoch); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.LogStartOffset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.LogEndOffset); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.CheckpointHW); err != nil {
		return nil, err
	}
	if resp.CommitReady {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	return buf.Bytes(), nil
}

func decodeReconcileProbeResponse(data []byte) (runtime.ReconcileProbeResponseEnvelope, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if version != reconcileProbeRespVerV2 && version != reconcileProbeRespVer {
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
	if err := binary.Read(rd, binary.BigEndian, &resp.LeaderEpoch); err != nil {
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
	if version == reconcileProbeRespVerV2 {
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
	var leader uint64
	if err := binary.Read(rd, binary.BigEndian, &leader); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	resp.Leader = channel.NodeID(leader)
	var role uint64
	if err := binary.Read(rd, binary.BigEndian, &role); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	resp.Role = channel.ReplicaRole(role)
	if err := binary.Read(rd, binary.BigEndian, &resp.OffsetEpoch); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.LogStartOffset); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.LogEndOffset); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.CheckpointHW); err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	commitReady, err := rd.ReadByte()
	if err != nil {
		return runtime.ReconcileProbeResponseEnvelope{}, err
	}
	switch commitReady {
	case 0:
		resp.CommitReady = false
	case 1:
		resp.CommitReady = true
	default:
		return runtime.ReconcileProbeResponseEnvelope{}, fmt.Errorf("channeltransport: invalid reconcile probe commit-ready flag %d", commitReady)
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
