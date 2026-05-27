package inspect

import (
	"math"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

const (
	scanModePointPartition    = "point-partition"
	scanModeExplicitPartition = "explicit-partition"
	scanModeLocalBounded      = "local-bounded"
	scanModeMessageChannel    = "message-channel"
	scanModeMessageCatalog    = "message-catalog"
)

type plan struct {
	Query         Query
	Domain        string
	TableName     string
	ScanMode      string
	HashSlot      uint16
	HashSlotSet   bool
	HashSlotCount uint16
	Cursor        *cursorPayload
}

var partitionKeys = map[string]string{
	"meta.user":                 "uid",
	"meta.device":               "uid",
	"meta.channel":              "channel_id",
	"meta.channel_runtime_meta": "channel_id",
	"meta.subscriber":           "channel_id",
	"meta.conversation":         "uid",
	"meta.cmd_conversation":     "uid",
	"meta.plugin_binding":       "uid",
	"meta.channel_migration":    "channel_id",
	"message.message":           "channel_key",
}

func planQuery(opts Options, q Query) (plan, error) {
	q.Limit = normalizeLimit(opts, q.Limit)
	domain, table, ok := splitQualifiedTable(q.Table)
	if !ok {
		return plan{}, ErrInvalidQuery
	}
	if !inspectTableExists(domain, table) {
		return plan{}, ErrInvalidQuery
	}
	if err := validateFilters(domain, table, q.Filters); err != nil {
		return plan{}, err
	}
	var cursor *cursorPayload
	if q.Cursor != "" {
		payload, err := decodeCursor(q.Cursor, q)
		if err != nil {
			return plan{}, err
		}
		cursor = &payload
	}
	p := plan{
		Query:         q,
		Domain:        domain,
		TableName:     table,
		HashSlotCount: opts.HashSlotCount,
		Cursor:        cursor,
	}
	var planned plan
	var err error
	switch domain {
	case "meta":
		planned, err = planMetaQuery(opts, p)
	case "message":
		planned, err = planMessageQuery(p)
	default:
		return plan{}, ErrInvalidQuery
	}
	if err != nil {
		return plan{}, err
	}
	if err := validateCursorForPlan(planned); err != nil {
		return plan{}, err
	}
	return planned, nil
}

func planMetaQuery(opts Options, p plan) (plan, error) {
	if value, ok := p.Query.Filters["hash_slot"]; ok {
		hashSlot, ok := asUint16(value)
		if !ok {
			return plan{}, ErrInvalidQuery
		}
		if opts.HashSlotCount > 0 && hashSlot >= opts.HashSlotCount {
			return plan{}, ErrInvalidQuery
		}
		p.ScanMode = scanModeExplicitPartition
		p.HashSlot = hashSlot
		p.HashSlotSet = true
		return p, nil
	}

	qualified := p.Domain + "." + p.TableName
	if keyName, ok := partitionKeys[qualified]; ok {
		if raw, exists := p.Query.Filters[keyName]; exists {
			value, ok := raw.(string)
			if !ok || value == "" {
				return plan{}, ErrInvalidQuery
			}
			if opts.HashSlotCount == 0 {
				return plan{}, ErrHashSlotRequired
			}
			p.ScanMode = scanModePointPartition
			p.HashSlot = cluster.HashSlotForKey(value, opts.HashSlotCount)
			p.HashSlotSet = true
			return p, nil
		}
	}

	if opts.HashSlotCount == 0 {
		return plan{}, ErrHashSlotRequired
	}
	p.ScanMode = scanModeLocalBounded
	return p, nil
}

func planMessageQuery(p plan) (plan, error) {
	switch p.TableName {
	case "channels":
		p.ScanMode = scanModeMessageCatalog
		return p, nil
	case "message":
		value, ok := p.Query.Filters["channel_key"].(string)
		if !ok || value == "" {
			return plan{}, ErrInvalidQuery
		}
		p.ScanMode = scanModeMessageChannel
		return p, nil
	default:
		return plan{}, ErrInvalidQuery
	}
}

func validateFilters(domain, table string, filters map[string]any) error {
	qualified := domain + "." + table
	columns, ok := inspectColumns[qualified]
	if !ok {
		return ErrInvalidQuery
	}
	allowed := make(map[string]struct{}, len(columns)+1)
	for _, column := range columns {
		allowed[column] = struct{}{}
	}
	if domain == "meta" {
		allowed["hash_slot"] = struct{}{}
	}
	for key := range filters {
		if _, ok := allowed[key]; !ok {
			return ErrInvalidQuery
		}
	}
	return nil
}

func validateCursorForPlan(p plan) error {
	if p.Cursor == nil {
		return nil
	}
	if p.Cursor.Domain != p.Domain || p.Cursor.Table != p.TableName || p.Cursor.ScanMode != p.ScanMode {
		return ErrCursorMismatch
	}
	if p.Domain == "meta" {
		if p.HashSlotCount > 0 && p.Cursor.HashSlot >= p.HashSlotCount {
			return ErrCursorMismatch
		}
		if p.HashSlotSet && p.Cursor.HashSlot != p.HashSlot {
			return ErrCursorMismatch
		}
	}
	if p.ScanMode == scanModeMessageChannel {
		channelKey, _ := p.Query.Filters["channel_key"].(string)
		if p.Cursor.ChannelKey != channelKey {
			return ErrCursorMismatch
		}
	}
	return nil
}

func inspectTableExists(domain, table string) bool {
	_, ok := inspectColumns[domain+"."+table]
	return ok
}

func normalizeLimit(opts Options, limit int) int {
	defaultValue := opts.DefaultLimit
	if defaultValue <= 0 {
		defaultValue = defaultLimit
	}
	maxValue := opts.MaxLimit
	if maxValue <= 0 {
		maxValue = maxLimit
	}
	if limit <= 0 {
		limit = defaultValue
	}
	if limit > maxValue {
		return maxValue
	}
	return limit
}

func splitQualifiedTable(table string) (string, string, bool) {
	parts := strings.Split(table, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func asUint16(value any) (uint16, bool) {
	switch v := value.(type) {
	case int:
		if v < 0 || v > math.MaxUint16 {
			return 0, false
		}
		return uint16(v), true
	case int8:
		if v < 0 {
			return 0, false
		}
		return uint16(v), true
	case int16:
		if v < 0 {
			return 0, false
		}
		return uint16(v), true
	case int32:
		if v < 0 || v > math.MaxUint16 {
			return 0, false
		}
		return uint16(v), true
	case int64:
		if v < 0 || v > math.MaxUint16 {
			return 0, false
		}
		return uint16(v), true
	case uint:
		if v > math.MaxUint16 {
			return 0, false
		}
		return uint16(v), true
	case uint8:
		return uint16(v), true
	case uint16:
		return v, true
	case uint32:
		if v > math.MaxUint16 {
			return 0, false
		}
		return uint16(v), true
	case uint64:
		if v > math.MaxUint16 {
			return 0, false
		}
		return uint16(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v < 0 || v > math.MaxUint16 {
			return 0, false
		}
		return uint16(v), true
	default:
		return 0, false
	}
}
