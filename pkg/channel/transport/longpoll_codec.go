package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func encodeLongPollFetchRequest(req LongPollFetchRequest) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 256))
	buf.WriteByte(longPollRequestCodecVer)
	if err := binary.Write(buf, binary.BigEndian, uint64(req.PeerID)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.LaneID); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.LaneCount); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.SessionID); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.SessionEpoch); err != nil {
		return nil, err
	}
	if err := buf.WriteByte(byte(req.Op)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.ProtocolVersion); err != nil {
		return nil, err
	}
	if err := buf.WriteByte(byte(req.Capabilities)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.MaxWaitMs); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.MaxBytes); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.MaxChannels); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, req.MembershipVersionHint); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(req.FullMembership))); err != nil {
		return nil, err
	}
	for _, member := range req.FullMembership {
		if err := writeChannelKey(buf, member.ChannelKey); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, member.ChannelEpoch); err != nil {
			return nil, err
		}
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(req.CursorDelta))); err != nil {
		return nil, err
	}
	for _, delta := range req.CursorDelta {
		if err := writeChannelKey(buf, delta.ChannelKey); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, delta.ChannelEpoch); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, delta.MatchOffset); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, delta.OffsetEpoch); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
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
	buf := bytes.NewBuffer(make([]byte, 0, 256))
	buf.WriteByte(longPollResponseCodecVer)
	if err := buf.WriteByte(byte(resp.Status)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.SessionID); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, resp.SessionEpoch); err != nil {
		return nil, err
	}
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
	if err := buf.WriteByte(flags); err != nil {
		return nil, err
	}
	if err := buf.WriteByte(byte(resp.ResetReason)); err != nil {
		return nil, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(len(resp.Items))); err != nil {
		return nil, err
	}
	for _, item := range resp.Items {
		if err := writeChannelKey(buf, item.ChannelKey); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, item.ChannelEpoch); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, item.LeaderEpoch); err != nil {
			return nil, err
		}
		if err := buf.WriteByte(byte(item.Flags)); err != nil {
			return nil, err
		}
		if err := binary.Write(buf, binary.BigEndian, item.LeaderHW); err != nil {
			return nil, err
		}
		if item.TruncateTo != nil {
			if err := buf.WriteByte(1); err != nil {
				return nil, err
			}
			if err := binary.Write(buf, binary.BigEndian, *item.TruncateTo); err != nil {
				return nil, err
			}
		} else {
			if err := buf.WriteByte(0); err != nil {
				return nil, err
			}
		}
		if err := binary.Write(buf, binary.BigEndian, uint32(len(item.Records))); err != nil {
			return nil, err
		}
		for _, record := range item.Records {
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
	}
	return buf.Bytes(), nil
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
		var recordCount uint32
		if err := binary.Read(rd, binary.BigEndian, &recordCount); err != nil {
			return LongPollFetchResponse{}, err
		}
		item.Records = make([]channel.Record, 0, recordCount)
		for j := uint32(0); j < recordCount; j++ {
			var sizeBytes int64
			if err := binary.Read(rd, binary.BigEndian, &sizeBytes); err != nil {
				return LongPollFetchResponse{}, err
			}
			var payloadLen uint32
			if err := binary.Read(rd, binary.BigEndian, &payloadLen); err != nil {
				return LongPollFetchResponse{}, err
			}
			payload := make([]byte, payloadLen)
			if _, err := io.ReadFull(rd, payload); err != nil {
				return LongPollFetchResponse{}, err
			}
			item.Records = append(item.Records, channel.Record{
				Payload:   payload,
				SizeBytes: int(sizeBytes),
			})
		}
		resp.Items = append(resp.Items, item)
	}
	if rd.Len() != 0 {
		return LongPollFetchResponse{}, fmt.Errorf("channeltransport: trailing long poll response payload bytes")
	}
	return resp, nil
}
