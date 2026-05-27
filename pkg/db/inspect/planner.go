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
	switch domain {
	case "meta":
		return planMetaQuery(opts, p)
	case "message":
		return planMessageQuery(p)
	default:
		return plan{}, ErrInvalidQuery
	}
}

func planMetaQuery(opts Options, p plan) (plan, error) {
	if value, ok := p.Query.Filters["hash_slot"]; ok {
		hashSlot, ok := asUint16(value)
		if !ok {
			return plan{}, ErrInvalidQuery
		}
		p.ScanMode = scanModeExplicitPartition
		p.HashSlot = hashSlot
		p.HashSlotSet = true
		return p, nil
	}

	qualified := p.Domain + "." + p.TableName
	if keyName, ok := partitionKeys[qualified]; ok {
		if value, ok := p.Query.Filters[keyName].(string); ok && value != "" {
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
