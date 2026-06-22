package transfer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const maxJSONLLineBytes = 64 * 1024 * 1024

// Uint64 preserves exact unsigned 64-bit values decoded from JSON numbers or decimal strings.
type Uint64 uint64

// UnmarshalJSON decodes an exact unsigned 64-bit integer from a JSON number or decimal string.
func (v *Uint64) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return fmt.Errorf("empty uint64")
	}
	if raw[0] == '"' {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return fmt.Errorf("decode uint64 string: %w", err)
		}
		raw = unquoted
	}
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("decode uint64 %q: %w", raw, err)
	}
	*v = Uint64(parsed)
	return nil
}

func readJSONL(ctx context.Context, r io.Reader, kind FileKind, visit func(any) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLLineBytes)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		record, err := decodeRecord(kind, line)
		if err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
		if err := visit(record); err != nil {
			return fmt.Errorf("line %d: visit: %w", lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan jsonl: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func decodeRecord(kind FileKind, line []byte) (any, error) {
	switch kind {
	case FileKindMetaUsers:
		var record UserRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("uid", record.UID); err != nil {
			return nil, err
		}
		return record, nil
	case FileKindMetaDevices:
		var record DeviceRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("uid", record.UID); err != nil {
			return nil, err
		}
		return record, nil
	case FileKindMetaChannels:
		var record ChannelRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("channel_id", record.ChannelID); err != nil {
			return nil, err
		}
		return record, nil
	case FileKindMetaSubscribers:
		var record SubscriberRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("channel_id", record.ChannelID); err != nil {
			return nil, err
		}
		if err := requireString("uid", record.UID); err != nil {
			return nil, err
		}
		return record, nil
	case FileKindMetaUserChannelMemberships:
		var record UserChannelMembershipRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("uid", record.UID); err != nil {
			return nil, err
		}
		if err := requireString("channel_id", record.ChannelID); err != nil {
			return nil, err
		}
		return record, nil
	case FileKindMetaConversations:
		var record ConversationRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("uid", record.UID); err != nil {
			return nil, err
		}
		if record.Kind != "normal" && record.Kind != "cmd" {
			return nil, fmt.Errorf("kind must be normal or cmd")
		}
		if err := requireString("channel_id", record.ChannelID); err != nil {
			return nil, err
		}
		return record, nil
	case FileKindMetaChannelLatest:
		var record ChannelLatestRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("channel_id", record.ChannelID); err != nil {
			return nil, err
		}
		payload, err := decodeBase64Field("last_payload_b64", record.LastPayloadB64)
		if err != nil {
			return nil, err
		}
		record.Payload = payload
		return record, nil
	case FileKindMessageChannels:
		var record MessageChannelRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("channel_key", record.ChannelKey); err != nil {
			return nil, err
		}
		if err := requireString("channel_id", record.ChannelID); err != nil {
			return nil, err
		}
		return record, nil
	case FileKindMessageMessages:
		var record MessageRecord
		if err := decodeStrict(line, &record); err != nil {
			return nil, err
		}
		if err := requireString("channel_key", record.ChannelKey); err != nil {
			return nil, err
		}
		if record.MessageSeq == 0 {
			return nil, fmt.Errorf("message_seq is required")
		}
		if record.MessageID == 0 {
			return nil, fmt.Errorf("message_id is required")
		}
		payload, err := decodeBase64Field("payload_b64", record.PayloadB64)
		if err != nil {
			return nil, err
		}
		record.Payload = payload
		return record, nil
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
}

func decodeStrict(line []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode record: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("decode record: extra JSON data")
	}
	return nil
}

func decodeBase64Field(name, raw string) ([]byte, error) {
	if raw == "" {
		return []byte{}, nil
	}
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: decode base64: %w", name, err)
	}
	return payload, nil
}

func requireString(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
