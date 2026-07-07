package fsm

import (
	"bytes"
	"encoding/binary"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	tagMessageEventChannelID   uint8 = 1
	tagMessageEventChannelType uint8 = 2
	tagMessageEventClientMsgNo uint8 = 3
	tagMessageEventEventID     uint8 = 4
	tagMessageEventEventKey    uint8 = 5
	tagMessageEventEventType   uint8 = 6
	tagMessageEventVisibility  uint8 = 7
	tagMessageEventOccurredAt  uint8 = 8
	tagMessageEventPayload     uint8 = 9
	tagMessageEventUpdatedAt   uint8 = 10
	tagMessageEventBatchEntry  uint8 = 11

	tagMessageEventResultChannelID   uint8 = 1
	tagMessageEventResultChannelType uint8 = 2
	tagMessageEventResultClientMsgNo uint8 = 3
	tagMessageEventResultEventID     uint8 = 4
	tagMessageEventResultEventKey    uint8 = 5
	tagMessageEventResultSeq         uint8 = 6
	tagMessageEventResultStatus      uint8 = 7
	tagMessageEventResultState       uint8 = 8
	tagMessageEventResultBatchEntry  uint8 = 9

	tagMessageEventStateChannelID       uint8 = 1
	tagMessageEventStateChannelType     uint8 = 2
	tagMessageEventStateClientMsgNo     uint8 = 3
	tagMessageEventStateEventKey        uint8 = 4
	tagMessageEventStateStatus          uint8 = 5
	tagMessageEventStateLastSeq         uint8 = 6
	tagMessageEventStateLastEventID     uint8 = 7
	tagMessageEventStateLastEventType   uint8 = 8
	tagMessageEventStateLastVisibility  uint8 = 9
	tagMessageEventStateLastOccurredAt  uint8 = 10
	tagMessageEventStateSnapshotPayload uint8 = 11
	tagMessageEventStateEndReason       uint8 = 12
	tagMessageEventStateError           uint8 = 13
	tagMessageEventStateUpdatedAt       uint8 = 14
)

var (
	messageEventAppendResultMagic  = [...]byte{'W', 'K', 'M', 'E', 1}
	messageEventAppendResultsMagic = [...]byte{'W', 'K', 'M', 'R', 1}
)

type appendMessageEventCmd struct {
	event  metadb.MessageEventAppend
	result metadb.MessageEventAppendResult
}

type appendMessageEventsBatchCmd struct {
	events  []metadb.MessageEventAppend
	result  metadb.MessageEventAppendResult
	results []metadb.MessageEventAppendResult
}

func (c *appendMessageEventCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	result, err := wb.AppendMessageEvent(hashSlot, c.event)
	if err != nil {
		return err
	}
	c.result = result
	return nil
}

func (c *appendMessageEventCmd) applyResult() []byte {
	return EncodeAppendMessageEventResult(c.result)
}

func (c *appendMessageEventsBatchCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	if len(c.events) == 0 {
		return metadb.ErrInvalidArgument
	}
	for _, event := range c.events {
		result, err := wb.AppendMessageEvent(hashSlot, event)
		if err != nil {
			return err
		}
		c.result = result
		c.results = append(c.results, result)
	}
	return nil
}

func (c *appendMessageEventsBatchCmd) applyResult() []byte {
	if len(c.results) > 1 {
		return EncodeAppendMessageEventResults(c.results)
	}
	return EncodeAppendMessageEventResult(c.result)
}

// EncodeAppendMessageEventCommand encodes one channel-owned message event append.
func EncodeAppendMessageEventCommand(event metadb.MessageEventAppend) []byte {
	buf := make([]byte, 0, headerSize+128+len(event.Payload))
	buf = append(buf, commandVersion, cmdTypeAppendMessageEvent)
	return appendMessageEventFields(buf, event)
}

func appendMessageEventFields(buf []byte, event metadb.MessageEventAppend) []byte {
	buf = appendStringTLVField(buf, tagMessageEventChannelID, event.ChannelID)
	buf = appendInt64TLVField(buf, tagMessageEventChannelType, event.ChannelType)
	buf = appendStringTLVField(buf, tagMessageEventClientMsgNo, event.ClientMsgNo)
	buf = appendStringTLVField(buf, tagMessageEventEventID, event.EventID)
	buf = appendStringTLVField(buf, tagMessageEventEventKey, event.EventKey)
	buf = appendStringTLVField(buf, tagMessageEventEventType, event.EventType)
	buf = appendStringTLVField(buf, tagMessageEventVisibility, event.Visibility)
	buf = appendInt64TLVField(buf, tagMessageEventOccurredAt, event.OccurredAt)
	buf = appendBytesTLVField(buf, tagMessageEventPayload, event.Payload)
	buf = appendInt64TLVField(buf, tagMessageEventUpdatedAt, event.UpdatedAt)
	return buf
}

// EncodeAppendMessageEventCommandChecked validates and encodes one event append command.
func EncodeAppendMessageEventCommandChecked(event metadb.MessageEventAppend) ([]byte, error) {
	if err := validateMessageEventAppend(event); err != nil {
		return nil, err
	}
	return EncodeAppendMessageEventCommand(event), nil
}

// EncodeAppendMessageEventsCommand encodes an ordered batch of channel-owned message event appends.
func EncodeAppendMessageEventsCommand(events []metadb.MessageEventAppend) []byte {
	capacity := headerSize
	for _, event := range events {
		capacity += tlvOverhead + 128 + len(event.Payload)
	}
	buf := make([]byte, 0, capacity)
	buf = append(buf, commandVersion, cmdTypeAppendMessageEventsBatch)
	for _, event := range events {
		entry := appendMessageEventFields(make([]byte, 0, 128+len(event.Payload)), event)
		buf = appendBytesTLVField(buf, tagMessageEventBatchEntry, entry)
	}
	return buf
}

// EncodeAppendMessageEventsCommandChecked validates and encodes an ordered event append batch.
func EncodeAppendMessageEventsCommandChecked(events []metadb.MessageEventAppend) ([]byte, error) {
	if err := validateMessageEventAppendBatch(events); err != nil {
		return nil, err
	}
	return EncodeAppendMessageEventsCommand(events), nil
}

// EncodeAppendMessageEventResult encodes the durable reducer result returned by Apply.
func EncodeAppendMessageEventResult(result metadb.MessageEventAppendResult) []byte {
	buf := make([]byte, 0, len(messageEventAppendResultMagic)+128+len(result.State.SnapshotPayload))
	buf = append(buf, messageEventAppendResultMagic[:]...)
	return appendMessageEventResultFields(buf, result)
}

func appendMessageEventResultFields(buf []byte, result metadb.MessageEventAppendResult) []byte {
	buf = appendStringTLVField(buf, tagMessageEventResultChannelID, result.ChannelID)
	buf = appendInt64TLVField(buf, tagMessageEventResultChannelType, result.ChannelType)
	buf = appendStringTLVField(buf, tagMessageEventResultClientMsgNo, result.ClientMsgNo)
	buf = appendStringTLVField(buf, tagMessageEventResultEventID, result.EventID)
	buf = appendStringTLVField(buf, tagMessageEventResultEventKey, result.EventKey)
	buf = appendUint64TLVField(buf, tagMessageEventResultSeq, result.MsgEventSeq)
	buf = appendStringTLVField(buf, tagMessageEventResultStatus, result.Status)
	buf = appendBytesTLVField(buf, tagMessageEventResultState, encodeMessageEventStateResult(result.State))
	return buf
}

// EncodeAppendMessageEventResults encodes per-event reducer results returned by a batch append.
func EncodeAppendMessageEventResults(results []metadb.MessageEventAppendResult) []byte {
	if len(results) == 1 {
		return EncodeAppendMessageEventResult(results[0])
	}
	capacity := len(messageEventAppendResultsMagic)
	for _, result := range results {
		capacity += tlvOverhead + 128 + len(result.State.SnapshotPayload)
	}
	buf := make([]byte, 0, capacity)
	buf = append(buf, messageEventAppendResultsMagic[:]...)
	for _, result := range results {
		entry := appendMessageEventResultFields(make([]byte, 0, 128+len(result.State.SnapshotPayload)), result)
		buf = appendBytesTLVField(buf, tagMessageEventResultBatchEntry, entry)
	}
	return buf
}

// DecodeAppendMessageEventResult decodes Slot FSM apply bytes for an event append.
func DecodeAppendMessageEventResult(data []byte) (metadb.MessageEventAppendResult, error) {
	results, err := DecodeAppendMessageEventResults(data)
	if err != nil {
		return metadb.MessageEventAppendResult{}, err
	}
	return results[len(results)-1], nil
}

// DecodeAppendMessageEventResults decodes one or more Slot FSM apply results for event appends.
func DecodeAppendMessageEventResults(data []byte) ([]metadb.MessageEventAppendResult, error) {
	if len(data) >= len(messageEventAppendResultsMagic) && bytes.Equal(data[:len(messageEventAppendResultsMagic)], messageEventAppendResultsMagic[:]) {
		results := make([]metadb.MessageEventAppendResult, 0, 4)
		off := len(messageEventAppendResultsMagic)
		for off < len(data) {
			tag, value, n, err := readTLV(data[off:])
			if err != nil {
				return nil, err
			}
			off += n
			switch tag {
			case tagMessageEventResultBatchEntry:
				result, err := decodeAppendMessageEventResultFields(value)
				if err != nil {
					return nil, err
				}
				results = append(results, result)
			default:
				// Unknown tag -- skip for forward compatibility.
			}
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("%w: empty message event append results", metadb.ErrCorruptValue)
		}
		return results, nil
	}
	result, err := decodeAppendMessageEventResult(data)
	if err != nil {
		return nil, err
	}
	return []metadb.MessageEventAppendResult{result}, nil
}

func decodeAppendMessageEventResult(data []byte) (metadb.MessageEventAppendResult, error) {
	if len(data) < len(messageEventAppendResultMagic) || !bytes.Equal(data[:len(messageEventAppendResultMagic)], messageEventAppendResultMagic[:]) {
		return metadb.MessageEventAppendResult{}, fmt.Errorf("%w: message event append result", metadb.ErrCorruptValue)
	}
	return decodeAppendMessageEventResultFields(data[len(messageEventAppendResultMagic):])
}

func decodeAppendMessageEventResultFields(data []byte) (metadb.MessageEventAppendResult, error) {
	var result metadb.MessageEventAppendResult
	var haveChannelID, haveChannelType, haveClientMsgNo, haveEventID, haveEventKey, haveSeq, haveStatus, haveState bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.MessageEventAppendResult{}, err
		}
		off += n
		switch tag {
		case tagMessageEventResultChannelID:
			result.ChannelID = string(value)
			haveChannelID = true
		case tagMessageEventResultChannelType:
			v, err := decodeInt64TLV(value, "message event result ChannelType")
			if err != nil {
				return metadb.MessageEventAppendResult{}, err
			}
			result.ChannelType = v
			haveChannelType = true
		case tagMessageEventResultClientMsgNo:
			result.ClientMsgNo = string(value)
			haveClientMsgNo = true
		case tagMessageEventResultEventID:
			result.EventID = string(value)
			haveEventID = true
		case tagMessageEventResultEventKey:
			result.EventKey = string(value)
			haveEventKey = true
		case tagMessageEventResultSeq:
			v, err := decodeUint64TLV(value, "message event result MsgEventSeq")
			if err != nil {
				return metadb.MessageEventAppendResult{}, err
			}
			result.MsgEventSeq = v
			haveSeq = true
		case tagMessageEventResultStatus:
			result.Status = string(value)
			haveStatus = true
		case tagMessageEventResultState:
			state, err := decodeMessageEventStateResult(value)
			if err != nil {
				return metadb.MessageEventAppendResult{}, err
			}
			result.State = state
			haveState = true
		default:
			// Unknown tag -- skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType || !haveClientMsgNo || !haveEventID || !haveEventKey || !haveSeq || !haveStatus || !haveState {
		return metadb.MessageEventAppendResult{}, fmt.Errorf("%w: incomplete message event append result", metadb.ErrCorruptValue)
	}
	return result, nil
}

func decodeAppendMessageEvent(data []byte) (command, error) {
	event, err := decodeMessageEventAppendFields(data)
	if err != nil {
		return nil, err
	}
	return &appendMessageEventCmd{event: event}, nil
}

func decodeAppendMessageEventsBatch(data []byte) (command, error) {
	events := make([]metadb.MessageEventAppend, 0, 4)
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return nil, err
		}
		off += n
		switch tag {
		case tagMessageEventBatchEntry:
			event, err := decodeMessageEventAppendFields(value)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		default:
			// Unknown tag -- skip for forward compatibility.
		}
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("%w: empty message event append batch command", metadb.ErrCorruptValue)
	}
	if err := validateMessageEventAppendBatch(events); err != nil {
		return nil, err
	}
	return &appendMessageEventsBatchCmd{events: events}, nil
}

func decodeMessageEventAppendFields(data []byte) (metadb.MessageEventAppend, error) {
	var event metadb.MessageEventAppend
	var haveChannelID, haveChannelType, haveClientMsgNo, haveEventID, haveEventType bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.MessageEventAppend{}, err
		}
		off += n
		switch tag {
		case tagMessageEventChannelID:
			event.ChannelID = string(value)
			haveChannelID = true
		case tagMessageEventChannelType:
			v, err := decodeInt64TLV(value, "message event ChannelType")
			if err != nil {
				return metadb.MessageEventAppend{}, err
			}
			event.ChannelType = v
			haveChannelType = true
		case tagMessageEventClientMsgNo:
			event.ClientMsgNo = string(value)
			haveClientMsgNo = true
		case tagMessageEventEventID:
			event.EventID = string(value)
			haveEventID = true
		case tagMessageEventEventKey:
			event.EventKey = string(value)
		case tagMessageEventEventType:
			event.EventType = string(value)
			haveEventType = true
		case tagMessageEventVisibility:
			event.Visibility = string(value)
		case tagMessageEventOccurredAt:
			v, err := decodeInt64TLV(value, "message event OccurredAt")
			if err != nil {
				return metadb.MessageEventAppend{}, err
			}
			event.OccurredAt = v
		case tagMessageEventPayload:
			event.Payload = append([]byte(nil), value...)
		case tagMessageEventUpdatedAt:
			v, err := decodeInt64TLV(value, "message event UpdatedAt")
			if err != nil {
				return metadb.MessageEventAppend{}, err
			}
			event.UpdatedAt = v
		default:
			// Unknown tag -- skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType || !haveClientMsgNo || !haveEventID || !haveEventType {
		return metadb.MessageEventAppend{}, fmt.Errorf("%w: incomplete message event append command", metadb.ErrCorruptValue)
	}
	return event, nil
}

func validateMessageEventAppend(event metadb.MessageEventAppend) error {
	if event.ChannelID == "" || event.ChannelType <= 0 || event.ClientMsgNo == "" || event.EventID == "" || event.EventType == "" {
		return metadb.ErrInvalidArgument
	}
	switch event.EventType {
	case metadb.EventTypeStreamOpen,
		metadb.EventTypeStreamDelta,
		metadb.EventTypeStreamClose,
		metadb.EventTypeStreamError,
		metadb.EventTypeStreamCancel,
		metadb.EventTypeStreamSnapshot,
		metadb.EventTypeStreamFinish:
		return nil
	default:
		return metadb.ErrInvalidArgument
	}
}

func validateMessageEventAppendBatch(events []metadb.MessageEventAppend) error {
	if len(events) == 0 {
		return metadb.ErrInvalidArgument
	}
	first := events[0]
	for _, event := range events {
		if err := validateMessageEventAppend(event); err != nil {
			return err
		}
		if event.ChannelID != first.ChannelID || event.ChannelType != first.ChannelType {
			return metadb.ErrInvalidArgument
		}
	}
	return nil
}

func encodeMessageEventStateResult(state metadb.MessageEventState) []byte {
	buf := make([]byte, 0, 128+len(state.SnapshotPayload))
	buf = appendStringTLVField(buf, tagMessageEventStateChannelID, state.ChannelID)
	buf = appendInt64TLVField(buf, tagMessageEventStateChannelType, state.ChannelType)
	buf = appendStringTLVField(buf, tagMessageEventStateClientMsgNo, state.ClientMsgNo)
	buf = appendStringTLVField(buf, tagMessageEventStateEventKey, state.EventKey)
	buf = appendStringTLVField(buf, tagMessageEventStateStatus, state.Status)
	buf = appendUint64TLVField(buf, tagMessageEventStateLastSeq, state.LastMsgEventSeq)
	buf = appendStringTLVField(buf, tagMessageEventStateLastEventID, state.LastEventID)
	buf = appendStringTLVField(buf, tagMessageEventStateLastEventType, state.LastEventType)
	buf = appendStringTLVField(buf, tagMessageEventStateLastVisibility, state.LastVisibility)
	buf = appendInt64TLVField(buf, tagMessageEventStateLastOccurredAt, state.LastOccurredAt)
	buf = appendBytesTLVField(buf, tagMessageEventStateSnapshotPayload, state.SnapshotPayload)
	buf = appendUint64TLVField(buf, tagMessageEventStateEndReason, uint64(state.EndReason))
	buf = appendStringTLVField(buf, tagMessageEventStateError, state.Error)
	buf = appendInt64TLVField(buf, tagMessageEventStateUpdatedAt, state.UpdatedAt)
	return buf
}

func decodeMessageEventStateResult(data []byte) (metadb.MessageEventState, error) {
	var state metadb.MessageEventState
	var haveChannelID, haveChannelType, haveClientMsgNo, haveEventKey, haveStatus, haveSeq bool
	off := 0
	for off < len(data) {
		tag, value, n, err := readTLV(data[off:])
		if err != nil {
			return metadb.MessageEventState{}, err
		}
		off += n
		switch tag {
		case tagMessageEventStateChannelID:
			state.ChannelID = string(value)
			haveChannelID = true
		case tagMessageEventStateChannelType:
			v, err := decodeInt64TLV(value, "message event state ChannelType")
			if err != nil {
				return metadb.MessageEventState{}, err
			}
			state.ChannelType = v
			haveChannelType = true
		case tagMessageEventStateClientMsgNo:
			state.ClientMsgNo = string(value)
			haveClientMsgNo = true
		case tagMessageEventStateEventKey:
			state.EventKey = string(value)
			haveEventKey = true
		case tagMessageEventStateStatus:
			state.Status = string(value)
			haveStatus = true
		case tagMessageEventStateLastSeq:
			v, err := decodeUint64TLV(value, "message event state LastMsgEventSeq")
			if err != nil {
				return metadb.MessageEventState{}, err
			}
			state.LastMsgEventSeq = v
			haveSeq = true
		case tagMessageEventStateLastEventID:
			state.LastEventID = string(value)
		case tagMessageEventStateLastEventType:
			state.LastEventType = string(value)
		case tagMessageEventStateLastVisibility:
			state.LastVisibility = string(value)
		case tagMessageEventStateLastOccurredAt:
			v, err := decodeInt64TLV(value, "message event state LastOccurredAt")
			if err != nil {
				return metadb.MessageEventState{}, err
			}
			state.LastOccurredAt = v
		case tagMessageEventStateSnapshotPayload:
			state.SnapshotPayload = append([]byte(nil), value...)
		case tagMessageEventStateEndReason:
			v, err := decodeUint64TLV(value, "message event state EndReason")
			if err != nil {
				return metadb.MessageEventState{}, err
			}
			if v > uint64(^uint8(0)) {
				return metadb.MessageEventState{}, fmt.Errorf("%w: bad message event state EndReason value %d", metadb.ErrCorruptValue, v)
			}
			state.EndReason = uint8(v)
		case tagMessageEventStateError:
			state.Error = string(value)
		case tagMessageEventStateUpdatedAt:
			v, err := decodeInt64TLV(value, "message event state UpdatedAt")
			if err != nil {
				return metadb.MessageEventState{}, err
			}
			state.UpdatedAt = v
		default:
			// Unknown tag -- skip for forward compatibility.
		}
	}
	if !haveChannelID || !haveChannelType || !haveClientMsgNo || !haveEventKey || !haveStatus || !haveSeq {
		return metadb.MessageEventState{}, fmt.Errorf("%w: incomplete message event state result", metadb.ErrCorruptValue)
	}
	return state, nil
}

func decodeInt64TLV(value []byte, label string) (int64, error) {
	if len(value) != 8 {
		return 0, fmt.Errorf("%w: bad %s length", metadb.ErrCorruptValue, label)
	}
	return int64(binary.BigEndian.Uint64(value)), nil
}

func decodeUint64TLV(value []byte, label string) (uint64, error) {
	if len(value) != 8 {
		return 0, fmt.Errorf("%w: bad %s length", metadb.ErrCorruptValue, label)
	}
	return binary.BigEndian.Uint64(value), nil
}
