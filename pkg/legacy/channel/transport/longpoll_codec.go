package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func encodeLongPollFetchRequest(req LongPollFetchRequest) ([]byte, error) {
	buf := make([]byte, 0, longPollFetchRequestEncodedSize(req))
	buf = append(buf, longPollRequestCodecVer)
	buf = binary.BigEndian.AppendUint64(buf, uint64(req.PeerID))
	buf = binary.BigEndian.AppendUint16(buf, req.LaneID)
	buf = binary.BigEndian.AppendUint16(buf, req.LaneCount)
	buf = binary.BigEndian.AppendUint64(buf, req.SessionID)
	buf = binary.BigEndian.AppendUint64(buf, req.SessionEpoch)
	buf = append(buf, byte(req.Op))
	buf = binary.BigEndian.AppendUint16(buf, req.ProtocolVersion)
	buf = append(buf, byte(req.Capabilities))
	buf = binary.BigEndian.AppendUint32(buf, req.MaxWaitMs)
	buf = binary.BigEndian.AppendUint32(buf, req.MaxBytes)
	buf = binary.BigEndian.AppendUint32(buf, req.MaxChannels)
	buf = binary.BigEndian.AppendUint64(buf, req.MembershipVersionHint)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(req.FullMembership)))
	for _, member := range req.FullMembership {
		buf = appendChannelKeyBytes(buf, member.ChannelKey)
		buf = binary.BigEndian.AppendUint64(buf, member.ChannelEpoch)
		buf = binary.BigEndian.AppendUint64(buf, member.ChannelGeneration)
	}
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(req.CursorDelta)))
	for _, delta := range req.CursorDelta {
		buf = appendChannelKeyBytes(buf, delta.ChannelKey)
		buf = binary.BigEndian.AppendUint64(buf, delta.ChannelEpoch)
		buf = binary.BigEndian.AppendUint64(buf, delta.ChannelGeneration)
		buf = binary.BigEndian.AppendUint64(buf, delta.MatchOffset)
		buf = binary.BigEndian.AppendUint64(buf, delta.OffsetEpoch)
	}
	return buf, nil
}

func longPollFetchRequestEncodedSize(req LongPollFetchRequest) int {
	size := 61
	for _, member := range req.FullMembership {
		size += 20 + len(member.ChannelKey)
	}
	for _, delta := range req.CursorDelta {
		size += 36 + len(delta.ChannelKey)
	}
	return size
}

func appendChannelKeyBytes(dst []byte, channelKey channel.ChannelKey) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(channelKey)))
	return append(dst, string(channelKey)...)
}

func decodeLongPollFetchRequest(data []byte) (LongPollFetchRequest, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return LongPollFetchRequest{}, err
	}
	if version != longPollRequestCodecVer {
		return LongPollFetchRequest{}, fmt.Errorf("channeltransport: unknown long poll request codec version %d", version)
	}
	var req LongPollFetchRequest
	var peerID uint64
	if err := binary.Read(rd, binary.BigEndian, &peerID); err != nil {
		return LongPollFetchRequest{}, err
	}
	req.PeerID = channel.NodeID(peerID)
	if err := binary.Read(rd, binary.BigEndian, &req.LaneID); err != nil {
		return LongPollFetchRequest{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.LaneCount); err != nil {
		return LongPollFetchRequest{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.SessionID); err != nil {
		return LongPollFetchRequest{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.SessionEpoch); err != nil {
		return LongPollFetchRequest{}, err
	}
	op, err := rd.ReadByte()
	if err != nil {
		return LongPollFetchRequest{}, err
	}
	req.Op = LanePollOp(op)
	if err := binary.Read(rd, binary.BigEndian, &req.ProtocolVersion); err != nil {
		return LongPollFetchRequest{}, err
	}
	capability, err := rd.ReadByte()
	if err != nil {
		return LongPollFetchRequest{}, err
	}
	req.Capabilities = LongPollCapability(capability)
	if err := binary.Read(rd, binary.BigEndian, &req.MaxWaitMs); err != nil {
		return LongPollFetchRequest{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.MaxBytes); err != nil {
		return LongPollFetchRequest{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.MaxChannels); err != nil {
		return LongPollFetchRequest{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &req.MembershipVersionHint); err != nil {
		return LongPollFetchRequest{}, err
	}
	var membershipCount uint32
	if err := binary.Read(rd, binary.BigEndian, &membershipCount); err != nil {
		return LongPollFetchRequest{}, err
	}
	req.FullMembership = make([]LongPollMembership, 0, membershipCount)
	for i := uint32(0); i < membershipCount; i++ {
		channelKey, err := readChannelKey(rd)
		if err != nil {
			return LongPollFetchRequest{}, err
		}
		member := LongPollMembership{ChannelKey: channelKey}
		if err := binary.Read(rd, binary.BigEndian, &member.ChannelEpoch); err != nil {
			return LongPollFetchRequest{}, err
		}
		if err := binary.Read(rd, binary.BigEndian, &member.ChannelGeneration); err != nil {
			return LongPollFetchRequest{}, err
		}
		req.FullMembership = append(req.FullMembership, member)
	}
	var deltaCount uint32
	if err := binary.Read(rd, binary.BigEndian, &deltaCount); err != nil {
		return LongPollFetchRequest{}, err
	}
	req.CursorDelta = make([]LongPollCursorDelta, 0, deltaCount)
	for i := uint32(0); i < deltaCount; i++ {
		channelKey, err := readChannelKey(rd)
		if err != nil {
			return LongPollFetchRequest{}, err
		}
		delta := LongPollCursorDelta{ChannelKey: channelKey}
		if err := binary.Read(rd, binary.BigEndian, &delta.ChannelEpoch); err != nil {
			return LongPollFetchRequest{}, err
		}
		if err := binary.Read(rd, binary.BigEndian, &delta.ChannelGeneration); err != nil {
			return LongPollFetchRequest{}, err
		}
		if err := binary.Read(rd, binary.BigEndian, &delta.MatchOffset); err != nil {
			return LongPollFetchRequest{}, err
		}
		if err := binary.Read(rd, binary.BigEndian, &delta.OffsetEpoch); err != nil {
			return LongPollFetchRequest{}, err
		}
		req.CursorDelta = append(req.CursorDelta, delta)
	}
	if rd.Len() != 0 {
		return LongPollFetchRequest{}, fmt.Errorf("channeltransport: trailing long poll request payload bytes")
	}
	return req, nil
}

func encodeLongPollFetchResponse(resp LongPollFetchResponse) ([]byte, error) {
	buf := make([]byte, 0, longPollFetchResponseEncodedSize(resp))
	buf = append(buf, longPollResponseCodecVer)
	buf = append(buf, byte(resp.Status))
	buf = binary.BigEndian.AppendUint64(buf, resp.SessionID)
	buf = binary.BigEndian.AppendUint64(buf, resp.SessionEpoch)
	var flags byte
	if resp.TimedOut {
		flags |= 1 << 0
	}
	if resp.MoreReady {
		flags |= 1 << 1
	}
	if resp.ResetRequired {
		flags |= 1 << 2
	}
	buf = append(buf, flags)
	buf = append(buf, byte(resp.ResetReason))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(resp.Items)))
	for _, item := range resp.Items {
		buf = appendChannelKeyBytes(buf, item.ChannelKey)
		buf = binary.BigEndian.AppendUint64(buf, item.ChannelEpoch)
		buf = binary.BigEndian.AppendUint64(buf, item.ChannelGeneration)
		buf = binary.BigEndian.AppendUint64(buf, item.LeaderEpoch)
		buf = append(buf, byte(item.Flags))
		buf = binary.BigEndian.AppendUint64(buf, item.LeaderHW)
		if item.TruncateTo != nil {
			buf = append(buf, 1)
			buf = binary.BigEndian.AppendUint64(buf, *item.TruncateTo)
		} else {
			buf = append(buf, 0)
		}
		buf = appendRetentionResetBytes(buf, item.RetentionReset)
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(item.Records)))
		for _, record := range item.Records {
			buf = appendRecordBytes(buf, record)
		}
	}
	return buf, nil
}

func longPollFetchResponseEncodedSize(resp LongPollFetchResponse) int {
	size := 24
	for _, item := range resp.Items {
		size += 42 + len(item.ChannelKey)
		if item.TruncateTo != nil {
			size += 8
		}
		size += retentionResetEncodedSize(item.RetentionReset)
		for _, record := range item.Records {
			size += recordEncodedSize(record)
		}
	}
	return size
}

func appendRetentionResetBytes(dst []byte, reset *channel.RetentionReset) []byte {
	if reset == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = binary.BigEndian.AppendUint64(dst, reset.RetentionThroughSeq)
	dst = binary.BigEndian.AppendUint64(dst, reset.RetainedThroughOffset)
	return binary.BigEndian.AppendUint64(dst, reset.MinAvailableSeq)
}

func retentionResetEncodedSize(reset *channel.RetentionReset) int {
	if reset == nil {
		return 1
	}
	return 25
}

func appendRecordBytes(dst []byte, record channel.Record) []byte {
	dst = binary.BigEndian.AppendUint64(dst, record.ID)
	dst = binary.BigEndian.AppendUint64(dst, record.Index)
	dst = binary.BigEndian.AppendUint64(dst, record.Epoch)
	dst = binary.BigEndian.AppendUint64(dst, uint64(record.SizeBytes))
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(record.Payload)))
	return append(dst, record.Payload...)
}

func decodeLongPollFetchResponse(data []byte) (LongPollFetchResponse, error) {
	rd := bytes.NewReader(data)
	version, err := rd.ReadByte()
	if err != nil {
		return LongPollFetchResponse{}, err
	}
	if version != longPollResponseCodecVer {
		return LongPollFetchResponse{}, fmt.Errorf("channeltransport: unknown long poll response codec version %d", version)
	}
	var resp LongPollFetchResponse
	status, err := rd.ReadByte()
	if err != nil {
		return LongPollFetchResponse{}, err
	}
	resp.Status = LanePollStatus(status)
	if err := binary.Read(rd, binary.BigEndian, &resp.SessionID); err != nil {
		return LongPollFetchResponse{}, err
	}
	if err := binary.Read(rd, binary.BigEndian, &resp.SessionEpoch); err != nil {
		return LongPollFetchResponse{}, err
	}
	flags, err := rd.ReadByte()
	if err != nil {
		return LongPollFetchResponse{}, err
	}
	resp.TimedOut = flags&(1<<0) != 0
	resp.MoreReady = flags&(1<<1) != 0
	resp.ResetRequired = flags&(1<<2) != 0
	resetReason, err := rd.ReadByte()
	if err != nil {
		return LongPollFetchResponse{}, err
	}
	resp.ResetReason = LongPollResetReason(resetReason)
	var itemCount uint32
	if err := binary.Read(rd, binary.BigEndian, &itemCount); err != nil {
		return LongPollFetchResponse{}, err
	}
	resp.Items = make([]LongPollItem, 0, itemCount)
	for i := uint32(0); i < itemCount; i++ {
		channelKey, err := readChannelKey(rd)
		if err != nil {
			return LongPollFetchResponse{}, err
		}
		item := LongPollItem{ChannelKey: channelKey}
		if err := binary.Read(rd, binary.BigEndian, &item.ChannelEpoch); err != nil {
			return LongPollFetchResponse{}, err
		}
		if err := binary.Read(rd, binary.BigEndian, &item.ChannelGeneration); err != nil {
			return LongPollFetchResponse{}, err
		}
		if err := binary.Read(rd, binary.BigEndian, &item.LeaderEpoch); err != nil {
			return LongPollFetchResponse{}, err
		}
		itemFlags, err := rd.ReadByte()
		if err != nil {
			return LongPollFetchResponse{}, err
		}
		item.Flags = LongPollItemFlags(itemFlags)
		if err := binary.Read(rd, binary.BigEndian, &item.LeaderHW); err != nil {
			return LongPollFetchResponse{}, err
		}
		hasTruncate, err := rd.ReadByte()
		if err != nil {
			return LongPollFetchResponse{}, err
		}
		if hasTruncate == 1 {
			var truncateTo uint64
			if err := binary.Read(rd, binary.BigEndian, &truncateTo); err != nil {
				return LongPollFetchResponse{}, err
			}
			item.TruncateTo = &truncateTo
		}
		reset, err := readRetentionReset(rd)
		if err != nil {
			return LongPollFetchResponse{}, err
		}
		item.RetentionReset = reset
		var recordCount uint32
		if err := binary.Read(rd, binary.BigEndian, &recordCount); err != nil {
			return LongPollFetchResponse{}, err
		}
		item.Records = make([]channel.Record, 0, recordCount)
		for j := uint32(0); j < recordCount; j++ {
			record, err := readRecord(rd)
			if err != nil {
				return LongPollFetchResponse{}, err
			}
			item.Records = append(item.Records, record)
		}
		resp.Items = append(resp.Items, item)
	}
	if rd.Len() != 0 {
		return LongPollFetchResponse{}, fmt.Errorf("channeltransport: trailing long poll response payload bytes")
	}
	return resp, nil
}
