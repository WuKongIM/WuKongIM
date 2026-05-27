package inspect

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type cursorPayload struct {
	Version    int    `json:"version"`
	Domain     string `json:"domain"`
	Table      string `json:"table"`
	ScanMode   string `json:"scan_mode"`
	HashSlot   uint16 `json:"hash_slot,omitempty"`
	Primary    []any  `json:"primary,omitempty"`
	ChannelKey string `json:"channel_key,omitempty"`
	AfterSeq   uint64 `json:"after_seq,omitempty"`
	QueryHash  string `json:"query_hash"`
}

func encodeCursor(payload cursorPayload) (string, error) {
	payload.Version = 1
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(raw string, q Query) (cursorPayload, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursorPayload{}, fmt.Errorf("%w: malformed cursor", ErrInvalidQuery)
	}
	var payload cursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return cursorPayload{}, fmt.Errorf("%w: malformed cursor", ErrInvalidQuery)
	}
	if payload.Version != 1 {
		return cursorPayload{}, fmt.Errorf("%w: unsupported cursor version", ErrInvalidQuery)
	}
	if payload.QueryHash != queryHash(q) {
		return cursorPayload{}, ErrCursorMismatch
	}
	return payload, nil
}

func queryHash(q Query) string {
	var b strings.Builder
	b.WriteString(q.Table)
	b.WriteByte('|')
	columns := append([]string(nil), q.Columns...)
	sort.Strings(columns)
	for _, column := range columns {
		b.WriteString(column)
		b.WriteByte(',')
	}
	b.WriteByte('|')
	keys := make([]string, 0, len(q.Filters))
	for key := range q.Filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%T:%v", q.Filters[key], q.Filters[key]))
		b.WriteByte(',')
	}
	b.WriteByte('|')
	b.WriteString(fmt.Sprintf("limit=%d", q.Limit))
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
