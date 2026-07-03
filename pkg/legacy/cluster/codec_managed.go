package cluster

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	managedSlotCodecVersion      byte = 1
	managedSlotRequestHeaderSize int  = 23

	managedSlotFlagNotLeader byte = 1 << iota
	managedSlotFlagNotFound
	managedSlotFlagTimeout

	managedSlotKindUnknown byte = iota
	managedSlotKindStatus
	managedSlotKindLogs
	managedSlotKindChangeConfig
	managedSlotKindTransferLeader
	managedSlotKindImportSnapshot
	managedSlotKindCompact

	managedSlotDecodedNil byte = iota
	managedSlotDecodedString
	managedSlotDecodedBool
	managedSlotDecodedInt64
	managedSlotDecodedUint64
	managedSlotDecodedMap
	managedSlotDecodedUint64Slice
	managedSlotDecodedStringSlice
	managedSlotDecodedMapSlice
	managedSlotDecodedAnySlice
)

var managedSlotCompactionPayloadMagic = []byte("WKSC")

func encodeManagedSlotRequest(req managedSlotRPCRequest) ([]byte, error) {
	kind, err := managedSlotKindCode(req.Kind)
	if err != nil {
		return nil, err
	}

	payload, err := encodeManagedSlotRequestPayload(req)
	if err != nil {
		return nil, err
	}

	body := make([]byte, 0, managedSlotRequestHeaderSize+binary.MaxVarintLen64+len(payload))
	header := make([]byte, managedSlotRequestHeaderSize)
	header[0] = managedSlotCodecVersion
	header[1] = kind
	binary.BigEndian.PutUint32(header[2:6], req.SlotID)
	binary.BigEndian.PutUint64(header[6:14], req.TargetNode)
	header[14] = byte(req.ChangeType)
	binary.BigEndian.PutUint64(header[15:23], req.NodeID)
	body = append(body, header...)
	body = binary.AppendUvarint(body, uint64(len(payload)))
	body = append(body, payload...)
	return body, nil
}

func decodeManagedSlotRequest(body []byte) (managedSlotRPCRequest, error) {
	if len(body) < managedSlotRequestHeaderSize {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}
	if body[0] != managedSlotCodecVersion {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}
	kind, err := managedSlotKindName(body[1])
	if err != nil {
		return managedSlotRPCRequest{}, err
	}
	payloadLen, n := binary.Uvarint(body[managedSlotRequestHeaderSize:])
	if n <= 0 {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}
	offset := managedSlotRequestHeaderSize + n
	if len(body[offset:]) != int(payloadLen) {
		return managedSlotRPCRequest{}, ErrInvalidConfig
	}

	req := managedSlotRPCRequest{
		Kind:       kind,
		SlotID:     binary.BigEndian.Uint32(body[2:6]),
		TargetNode: binary.BigEndian.Uint64(body[6:14]),
		ChangeType: multiraft.ChangeType(body[14]),
		NodeID:     binary.BigEndian.Uint64(body[15:23]),
	}
	if err := decodeManagedSlotRequestPayload(&req, body[offset:]); err != nil {
		return managedSlotRPCRequest{}, err
	}
	return req, nil
}

func encodeManagedSlotRequestPayload(req managedSlotRPCRequest) ([]byte, error) {
	switch req.Kind {
	case managedSlotRPCStatus, managedSlotRPCChangeConfig, managedSlotRPCTransferLeader, managedSlotRPCCompact:
		return nil, nil
	case managedSlotRPCLogs:
		body := make([]byte, 0, binary.MaxVarintLen64*2)
		body = binary.AppendUvarint(body, req.Limit)
		body = binary.AppendUvarint(body, req.Cursor)
		return body, nil
	case managedSlotRPCImportSnapshot:
		body := make([]byte, 0, 2+len(req.Snapshot))
		body = binary.BigEndian.AppendUint16(body, req.HashSlot)
		body = append(body, req.Snapshot...)
		return body, nil
	default:
		return nil, ErrInvalidConfig
	}
}

func decodeManagedSlotRequestPayload(req *managedSlotRPCRequest, payload []byte) error {
	switch req.Kind {
	case managedSlotRPCStatus, managedSlotRPCChangeConfig, managedSlotRPCTransferLeader, managedSlotRPCCompact:
		if len(payload) != 0 {
			return ErrInvalidConfig
		}
		return nil
	case managedSlotRPCLogs:
		limit, rest, err := readUvarint(payload)
		if err != nil {
			return err
		}
		cursor, rest, err := readUvarint(rest)
		if err != nil {
			return err
		}
		if len(rest) != 0 {
			return ErrInvalidConfig
		}
		req.Limit = limit
		req.Cursor = cursor
		return nil
	case managedSlotRPCImportSnapshot:
		if len(payload) < 2 {
			return ErrInvalidConfig
		}
		req.HashSlot = binary.BigEndian.Uint16(payload[:2])
		req.Snapshot = append([]byte(nil), payload[2:]...)
		return nil
	default:
		return ErrInvalidConfig
	}
}

func encodeManagedSlotResponse(resp managedSlotRPCResponse) ([]byte, error) {
	flags := byte(0)
	if resp.NotLeader {
		flags |= managedSlotFlagNotLeader
	}
	if resp.NotFound {
		flags |= managedSlotFlagNotFound
	}
	if resp.Timeout {
		flags |= managedSlotFlagTimeout
	}

	message := []byte(resp.Message)
	body := make([]byte, 0, 1+1+8+8+8+binary.MaxVarintLen64+len(message)+binary.MaxVarintLen64+len(resp.CurrentVoters)*8)
	body = append(body, managedSlotCodecVersion, flags)

	var fixed [24]byte
	binary.BigEndian.PutUint64(fixed[0:8], resp.LeaderID)
	binary.BigEndian.PutUint64(fixed[8:16], resp.CommitIndex)
	binary.BigEndian.PutUint64(fixed[16:24], resp.AppliedIndex)
	body = append(body, fixed[:]...)
	body = binary.AppendUvarint(body, uint64(len(message)))
	body = append(body, message...)
	body = appendUint64Slice(body, resp.CurrentVoters)
	hasLogPayload := resp.FirstIndex != 0 || resp.LastIndex != 0 || resp.NextCursor != 0 || len(resp.LogEntries) > 0
	if hasLogPayload && resp.Compaction != nil {
		return nil, ErrInvalidConfig
	}
	if hasLogPayload {
		var err error
		body, err = appendManagedSlotLogEntriesPayload(body, resp)
		if err != nil {
			return nil, err
		}
	}
	if resp.Compaction != nil {
		body = appendManagedSlotCompactionPayload(body, *resp.Compaction)
	}
	return body, nil
}

func decodeManagedSlotResponse(body []byte) (managedSlotRPCResponse, error) {
	if len(body) < 26 {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}
	if body[0] != managedSlotCodecVersion {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}

	resp := managedSlotRPCResponse{
		NotLeader:    body[1]&managedSlotFlagNotLeader != 0,
		NotFound:     body[1]&managedSlotFlagNotFound != 0,
		Timeout:      body[1]&managedSlotFlagTimeout != 0,
		LeaderID:     binary.BigEndian.Uint64(body[2:10]),
		CommitIndex:  binary.BigEndian.Uint64(body[10:18]),
		AppliedIndex: binary.BigEndian.Uint64(body[18:26]),
	}
	messageLen, n := binary.Uvarint(body[26:])
	if n <= 0 {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}
	offset := 26 + n
	if len(body[offset:]) < int(messageLen) {
		return managedSlotRPCResponse{}, ErrInvalidConfig
	}
	messageEnd := offset + int(messageLen)
	resp.Message = string(body[offset:messageEnd])
	rest := body[messageEnd:]
	if len(rest) > 0 {
		currentVoters, next, err := readUint64Slice(rest)
		if err != nil {
			return managedSlotRPCResponse{}, ErrInvalidConfig
		}
		resp.CurrentVoters = currentVoters
		rest = next
	}
	if len(rest) > 0 && bytes.HasPrefix(rest, managedSlotCompactionPayloadMagic) {
		result, err := decodeManagedSlotCompactionPayload(rest)
		if err != nil {
			return managedSlotRPCResponse{}, err
		}
		resp.Compaction = &result
		rest = nil
	}
	if len(rest) > 0 {
		if err := decodeManagedSlotLogEntriesPayload(&resp, rest); err != nil {
			return managedSlotRPCResponse{}, err
		}
	}

	switch {
	case resp.NotLeader:
		return resp, ErrNotLeader
	case resp.NotFound:
		return resp, ErrSlotNotFound
	case resp.Timeout:
		return resp, context.DeadlineExceeded
	case resp.Message != "":
		return resp, errors.New(resp.Message)
	default:
		return resp, nil
	}
}

func managedSlotKindCode(kind string) (byte, error) {
	switch kind {
	case managedSlotRPCStatus:
		return managedSlotKindStatus, nil
	case managedSlotRPCLogs:
		return managedSlotKindLogs, nil
	case managedSlotRPCChangeConfig:
		return managedSlotKindChangeConfig, nil
	case managedSlotRPCTransferLeader:
		return managedSlotKindTransferLeader, nil
	case managedSlotRPCImportSnapshot:
		return managedSlotKindImportSnapshot, nil
	case managedSlotRPCCompact:
		return managedSlotKindCompact, nil
	default:
		return managedSlotKindUnknown, ErrInvalidConfig
	}
}

func managedSlotKindName(kind byte) (string, error) {
	switch kind {
	case managedSlotKindStatus:
		return managedSlotRPCStatus, nil
	case managedSlotKindLogs:
		return managedSlotRPCLogs, nil
	case managedSlotKindChangeConfig:
		return managedSlotRPCChangeConfig, nil
	case managedSlotKindTransferLeader:
		return managedSlotRPCTransferLeader, nil
	case managedSlotKindImportSnapshot:
		return managedSlotRPCImportSnapshot, nil
	case managedSlotKindCompact:
		return managedSlotRPCCompact, nil
	default:
		return "", ErrInvalidConfig
	}
}

func appendManagedSlotCompactionPayload(dst []byte, result SlotRaftCompactionResult) []byte {
	dst = append(dst, managedSlotCompactionPayloadMagic...)
	dst = binary.BigEndian.AppendUint64(dst, result.NodeID)
	dst = binary.BigEndian.AppendUint32(dst, result.SlotID)
	dst = binary.BigEndian.AppendUint64(dst, result.AppliedIndex)
	dst = binary.BigEndian.AppendUint64(dst, result.BeforeSnapshotIndex)
	dst = binary.BigEndian.AppendUint64(dst, result.AfterSnapshotIndex)
	dst = appendBool(dst, result.Compacted)
	return appendString(dst, result.SkippedReason)
}

func decodeManagedSlotCompactionPayload(src []byte) (SlotRaftCompactionResult, error) {
	if !bytes.HasPrefix(src, managedSlotCompactionPayloadMagic) {
		return SlotRaftCompactionResult{}, ErrInvalidConfig
	}
	var result SlotRaftCompactionResult
	var err error
	rest := src[len(managedSlotCompactionPayloadMagic):]
	if result.NodeID, rest, err = readUint64(rest); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if result.SlotID, rest, err = readUint32(rest); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if result.AppliedIndex, rest, err = readUint64(rest); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if result.BeforeSnapshotIndex, rest, err = readUint64(rest); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if result.AfterSnapshotIndex, rest, err = readUint64(rest); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if result.Compacted, rest, err = readBool(rest); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if result.SkippedReason, rest, err = readString(rest); err != nil {
		return SlotRaftCompactionResult{}, err
	}
	if len(rest) != 0 {
		return SlotRaftCompactionResult{}, ErrInvalidConfig
	}
	return result, nil
}

func appendManagedSlotLogEntriesPayload(dst []byte, resp managedSlotRPCResponse) ([]byte, error) {
	var fixed [24]byte
	binary.BigEndian.PutUint64(fixed[0:8], resp.FirstIndex)
	binary.BigEndian.PutUint64(fixed[8:16], resp.LastIndex)
	binary.BigEndian.PutUint64(fixed[16:24], resp.NextCursor)
	dst = append(dst, fixed[:]...)
	dst = binary.AppendUvarint(dst, uint64(len(resp.LogEntries)))
	for _, entry := range resp.LogEntries {
		var entryFixed [16]byte
		binary.BigEndian.PutUint64(entryFixed[0:8], entry.Index)
		binary.BigEndian.PutUint64(entryFixed[8:16], entry.Term)
		dst = append(dst, entryFixed[:]...)
		dst = appendString(dst, entry.Type)
		dst = binary.AppendUvarint(dst, uint64(entry.DataSize))
		dst = appendString(dst, entry.DecodeStatus)
		dst = appendString(dst, entry.DecodedType)
		decoded, err := encodeManagedSlotLogDecoded(entry.Decoded)
		if err != nil {
			return nil, err
		}
		dst = appendBytes(dst, decoded)
	}
	return dst, nil
}

func decodeManagedSlotLogEntriesPayload(resp *managedSlotRPCResponse, src []byte) error {
	if len(src) < 24 {
		return ErrInvalidConfig
	}
	resp.FirstIndex = binary.BigEndian.Uint64(src[0:8])
	resp.LastIndex = binary.BigEndian.Uint64(src[8:16])
	resp.NextCursor = binary.BigEndian.Uint64(src[16:24])
	count, rest, err := readUvarint(src[24:])
	if err != nil {
		return err
	}
	entries := make([]managedSlotLogEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		if len(rest) < 16 {
			return ErrInvalidConfig
		}
		entry := managedSlotLogEntry{
			Index: binary.BigEndian.Uint64(rest[0:8]),
			Term:  binary.BigEndian.Uint64(rest[8:16]),
		}
		entry.Type, rest, err = readString(rest[16:])
		if err != nil {
			return err
		}
		dataSize, next, err := readUvarint(rest)
		if err != nil {
			return err
		}
		entry.DataSize = int(dataSize)
		entry.DecodeStatus, next, err = readString(next)
		if err != nil {
			return err
		}
		entry.DecodedType, next, err = readString(next)
		if err != nil {
			return err
		}
		decoded, next, err := readBytes(next)
		if err != nil {
			return err
		}
		if len(decoded) > 0 {
			if err := decodeManagedSlotLogDecoded(decoded, &entry.Decoded); err != nil {
				return err
			}
		}
		entries = append(entries, entry)
		rest = next
	}
	if len(rest) != 0 {
		return ErrInvalidConfig
	}
	resp.LogEntries = entries
	return nil
}

func encodeManagedSlotLogDecoded(decoded map[string]any) ([]byte, error) {
	if decoded == nil {
		return nil, nil
	}
	return appendManagedSlotDecodedMap(nil, decoded)
}

func decodeManagedSlotLogDecoded(src []byte, dst *map[string]any) error {
	decoded, rest, err := readManagedSlotDecodedMap(src)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return ErrInvalidConfig
	}
	*dst = decoded
	return nil
}

func appendManagedSlotDecodedMap(dst []byte, values map[string]any) ([]byte, error) {
	dst = binary.AppendUvarint(dst, uint64(len(values)))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var err error
		dst = appendString(dst, key)
		dst, err = appendManagedSlotDecodedValue(dst, values[key])
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func readManagedSlotDecodedMap(src []byte) (map[string]any, []byte, error) {
	count, rest, err := readUvarint(src)
	if err != nil {
		return nil, nil, err
	}
	if count > uint64(len(rest)) && count != 0 {
		return nil, nil, ErrInvalidConfig
	}
	values := make(map[string]any, count)
	for i := uint64(0); i < count; i++ {
		var key string
		key, rest, err = readString(rest)
		if err != nil {
			return nil, nil, err
		}
		var value any
		value, rest, err = readManagedSlotDecodedValue(rest)
		if err != nil {
			return nil, nil, err
		}
		values[key] = value
	}
	return values, rest, nil
}

func appendManagedSlotDecodedValue(dst []byte, value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return append(dst, managedSlotDecodedNil), nil
	case string:
		dst = append(dst, managedSlotDecodedString)
		return appendString(dst, v), nil
	case bool:
		dst = append(dst, managedSlotDecodedBool)
		if v {
			return append(dst, 1), nil
		}
		return append(dst, 0), nil
	case int:
		dst = append(dst, managedSlotDecodedInt64)
		return appendInt64(dst, int64(v)), nil
	case int8:
		dst = append(dst, managedSlotDecodedInt64)
		return appendInt64(dst, int64(v)), nil
	case int16:
		dst = append(dst, managedSlotDecodedInt64)
		return appendInt64(dst, int64(v)), nil
	case int32:
		dst = append(dst, managedSlotDecodedInt64)
		return appendInt64(dst, int64(v)), nil
	case int64:
		dst = append(dst, managedSlotDecodedInt64)
		return appendInt64(dst, v), nil
	case uint:
		dst = append(dst, managedSlotDecodedUint64)
		return binary.BigEndian.AppendUint64(dst, uint64(v)), nil
	case uint8:
		dst = append(dst, managedSlotDecodedUint64)
		return binary.BigEndian.AppendUint64(dst, uint64(v)), nil
	case uint16:
		dst = append(dst, managedSlotDecodedUint64)
		return binary.BigEndian.AppendUint64(dst, uint64(v)), nil
	case uint32:
		dst = append(dst, managedSlotDecodedUint64)
		return binary.BigEndian.AppendUint64(dst, uint64(v)), nil
	case uint64:
		dst = append(dst, managedSlotDecodedUint64)
		return binary.BigEndian.AppendUint64(dst, v), nil
	case map[string]any:
		dst = append(dst, managedSlotDecodedMap)
		return appendManagedSlotDecodedMap(dst, v)
	case []uint64:
		dst = append(dst, managedSlotDecodedUint64Slice)
		return appendUint64Slice(dst, v), nil
	case []string:
		dst = append(dst, managedSlotDecodedStringSlice)
		dst = binary.AppendUvarint(dst, uint64(len(v)))
		for _, item := range v {
			dst = appendString(dst, item)
		}
		return dst, nil
	case []map[string]any:
		dst = append(dst, managedSlotDecodedMapSlice)
		dst = binary.AppendUvarint(dst, uint64(len(v)))
		for _, item := range v {
			var err error
			dst, err = appendManagedSlotDecodedMap(dst, item)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	case []any:
		dst = append(dst, managedSlotDecodedAnySlice)
		dst = binary.AppendUvarint(dst, uint64(len(v)))
		for _, item := range v {
			var err error
			dst, err = appendManagedSlotDecodedValue(dst, item)
			if err != nil {
				return nil, err
			}
		}
		return dst, nil
	default:
		return nil, ErrInvalidConfig
	}
}

func readManagedSlotDecodedValue(src []byte) (any, []byte, error) {
	if len(src) == 0 {
		return nil, nil, ErrInvalidConfig
	}
	tag := src[0]
	rest := src[1:]
	switch tag {
	case managedSlotDecodedNil:
		return nil, rest, nil
	case managedSlotDecodedString:
		return readString(rest)
	case managedSlotDecodedBool:
		if len(rest) == 0 {
			return nil, nil, ErrInvalidConfig
		}
		switch rest[0] {
		case 0:
			return false, rest[1:], nil
		case 1:
			return true, rest[1:], nil
		default:
			return nil, nil, ErrInvalidConfig
		}
	case managedSlotDecodedInt64:
		return readInt64(rest)
	case managedSlotDecodedUint64:
		return readUint64(rest)
	case managedSlotDecodedMap:
		return readManagedSlotDecodedMap(rest)
	case managedSlotDecodedUint64Slice:
		return readUint64Slice(rest)
	case managedSlotDecodedStringSlice:
		return readManagedSlotDecodedStringSlice(rest)
	case managedSlotDecodedMapSlice:
		return readManagedSlotDecodedMapSlice(rest)
	case managedSlotDecodedAnySlice:
		return readManagedSlotDecodedAnySlice(rest)
	default:
		return nil, nil, ErrInvalidConfig
	}
}

func readManagedSlotDecodedStringSlice(src []byte) ([]string, []byte, error) {
	count, rest, err := readUvarint(src)
	if err != nil {
		return nil, nil, err
	}
	if count > uint64(len(rest)) && count != 0 {
		return nil, nil, ErrInvalidConfig
	}
	values := make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		var value string
		value, rest, err = readString(rest)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
	}
	return values, rest, nil
}

func readManagedSlotDecodedMapSlice(src []byte) ([]map[string]any, []byte, error) {
	count, rest, err := readUvarint(src)
	if err != nil {
		return nil, nil, err
	}
	if count > uint64(len(rest)) && count != 0 {
		return nil, nil, ErrInvalidConfig
	}
	values := make([]map[string]any, 0, count)
	for i := uint64(0); i < count; i++ {
		var value map[string]any
		value, rest, err = readManagedSlotDecodedMap(rest)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
	}
	return values, rest, nil
}

func readManagedSlotDecodedAnySlice(src []byte) ([]any, []byte, error) {
	count, rest, err := readUvarint(src)
	if err != nil {
		return nil, nil, err
	}
	if count > uint64(len(rest)) && count != 0 {
		return nil, nil, ErrInvalidConfig
	}
	values := make([]any, 0, count)
	for i := uint64(0); i < count; i++ {
		var value any
		value, rest, err = readManagedSlotDecodedValue(rest)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, value)
	}
	return values, rest, nil
}
