package backup

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
)

var channelIndexMagic = [...]byte{'W', 'K', 'B', 'I'}

const (
	channelIndexVersion    uint16 = 1
	maxChannelIndexEntries        = 1 << 20
	maxChannelIndexIDBytes        = 4096
	maxChannelIndexBytes          = 256 << 20
)

// ChannelBoundary is the committed durable state of one channel at a restore point.
type ChannelBoundary struct {
	// ChannelID is the durable business identity.
	ChannelID string
	// ChannelType is the durable business type.
	ChannelType uint8
	// Epoch is the committed Channel metadata epoch.
	Epoch uint64
	// LogStartOffset is the retained logical prefix boundary.
	LogStartOffset uint64
	// HW is the committed message high watermark.
	HW uint64
}

// MarshalChannelIndex serializes a bounded sorted per-partition channel index.
func MarshalChannelIndex(hashSlot uint16, boundaries []ChannelBoundary) ([]byte, error) {
	if len(boundaries) > maxChannelIndexEntries {
		return nil, fmt.Errorf("%w: channel index entry count exceeds limit", ErrInvalidManifest)
	}
	items := append([]ChannelBoundary(nil), boundaries...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].ChannelType != items[j].ChannelType {
			return items[i].ChannelType < items[j].ChannelType
		}
		return items[i].ChannelID < items[j].ChannelID
	})
	var payload bytes.Buffer
	payload.Write(channelIndexMagic[:])
	_ = binary.Write(&payload, binary.BigEndian, channelIndexVersion)
	_ = binary.Write(&payload, binary.BigEndian, hashSlot)
	_ = binary.Write(&payload, binary.BigEndian, uint32(len(items)))
	for index, item := range items {
		if err := validateChannelBoundary(item); err != nil {
			return nil, fmt.Errorf("%w: channel index[%d]: %v", ErrInvalidManifest, index, err)
		}
		if index > 0 && items[index-1].ChannelType == item.ChannelType && items[index-1].ChannelID == item.ChannelID {
			return nil, fmt.Errorf("%w: duplicate channel index identity", ErrInvalidManifest)
		}
		payload.WriteByte(item.ChannelType)
		_ = binary.Write(&payload, binary.BigEndian, uint16(len(item.ChannelID)))
		payload.WriteString(item.ChannelID)
		_ = binary.Write(&payload, binary.BigEndian, item.Epoch)
		_ = binary.Write(&payload, binary.BigEndian, item.LogStartOffset)
		_ = binary.Write(&payload, binary.BigEndian, item.HW)
		if payload.Len()+4 > maxChannelIndexBytes {
			return nil, fmt.Errorf("%w: channel index exceeds size limit", ErrInvalidManifest)
		}
	}
	body := payload.Bytes()
	checksum := crc32.ChecksumIEEE(body)
	result := append([]byte(nil), body...)
	result = binary.BigEndian.AppendUint32(result, checksum)
	return result, nil
}

// LoadChannelIndex verifies and decodes one per-partition committed-cut index.
func LoadChannelIndex(body []byte) (uint16, []ChannelBoundary, error) {
	if len(body) < 16 || len(body) > maxChannelIndexBytes {
		return 0, nil, fmt.Errorf("%w: channel index size is invalid", ErrInvalidManifest)
	}
	payload := body[:len(body)-4]
	if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(body[len(body)-4:]) {
		return 0, nil, fmt.Errorf("%w: channel index checksum mismatch", ErrObjectCorrupt)
	}
	reader := bytes.NewReader(payload)
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil || magic != channelIndexMagic {
		return 0, nil, fmt.Errorf("%w: channel index magic is invalid", ErrInvalidManifest)
	}
	var version, hashSlot uint16
	var count uint32
	if binary.Read(reader, binary.BigEndian, &version) != nil || version != channelIndexVersion ||
		binary.Read(reader, binary.BigEndian, &hashSlot) != nil || binary.Read(reader, binary.BigEndian, &count) != nil || count > maxChannelIndexEntries {
		return 0, nil, fmt.Errorf("%w: channel index header is invalid", ErrInvalidManifest)
	}
	items := make([]ChannelBoundary, 0, count)
	for index := uint32(0); index < count; index++ {
		channelType, err := reader.ReadByte()
		if err != nil {
			return 0, nil, fmt.Errorf("%w: channel index is truncated", ErrInvalidManifest)
		}
		var idBytes uint16
		if err := binary.Read(reader, binary.BigEndian, &idBytes); err != nil || idBytes == 0 || idBytes > maxChannelIndexIDBytes || int(idBytes) > reader.Len() {
			return 0, nil, fmt.Errorf("%w: channel index identity is invalid", ErrInvalidManifest)
		}
		id := make([]byte, idBytes)
		if _, err := io.ReadFull(reader, id); err != nil {
			return 0, nil, fmt.Errorf("%w: channel index is truncated", ErrInvalidManifest)
		}
		item := ChannelBoundary{ChannelID: string(id), ChannelType: channelType}
		if binary.Read(reader, binary.BigEndian, &item.Epoch) != nil || binary.Read(reader, binary.BigEndian, &item.LogStartOffset) != nil || binary.Read(reader, binary.BigEndian, &item.HW) != nil {
			return 0, nil, fmt.Errorf("%w: channel index is truncated", ErrInvalidManifest)
		}
		if err := validateChannelBoundary(item); err != nil {
			return 0, nil, fmt.Errorf("%w: channel index[%d]: %v", ErrInvalidManifest, index, err)
		}
		if len(items) > 0 {
			previous := items[len(items)-1]
			if previous.ChannelType > item.ChannelType || (previous.ChannelType == item.ChannelType && previous.ChannelID >= item.ChannelID) {
				return 0, nil, fmt.Errorf("%w: channel index is not strictly sorted", ErrInvalidManifest)
			}
		}
		items = append(items, item)
	}
	if reader.Len() != 0 {
		return 0, nil, fmt.Errorf("%w: trailing channel index data", ErrInvalidManifest)
	}
	return hashSlot, items, nil
}

func validateChannelBoundary(boundary ChannelBoundary) error {
	if boundary.ChannelID == "" || len(boundary.ChannelID) > maxChannelIndexIDBytes || boundary.Epoch == 0 || boundary.LogStartOffset > boundary.HW {
		return fmt.Errorf("invalid channel boundary")
	}
	return nil
}
