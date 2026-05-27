package meta

import (
	"context"
	"fmt"
	"math"
	"reflect"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const (
	inspectMaxInt64 = int64(^uint64(0) >> 1)
	inspectMinInt64 = -inspectMaxInt64 - 1
)

// InspectRow contains one decoded metadata row.
type InspectRow map[string]any

// InspectCursor identifies the next row position for an inspection scan.
type InspectCursor struct {
	// HashSlot is the hash slot where the next scan should resume.
	HashSlot HashSlot
	// Primary stores the table primary-key cursor values.
	Primary []any
}

// InspectScanRequest describes a bounded read-only metadata table scan.
type InspectScanRequest struct {
	// Table is the metadata table name to inspect.
	Table string
	// HashSlot selects one hash slot when HashSlotSet is true.
	HashSlot HashSlot
	// HashSlotSet reports whether HashSlot was explicitly selected.
	HashSlotSet bool
	// HashSlotCount bounds local scans when no explicit hash slot is selected.
	HashSlotCount uint16
	// Filters applies simple equality checks to decoded row fields.
	Filters map[string]any
	// After resumes the scan after a previous cursor.
	After *InspectCursor
	// Limit bounds the total returned rows.
	Limit int
}

// InspectScanResult contains rows and resume metadata from an inspection scan.
type InspectScanResult struct {
	// Rows contains decoded rows that matched the request filters.
	Rows []InspectRow
	// Next is the cursor to continue scanning when Done is false.
	Next *InspectCursor
	// Done reports whether the requested scan range was exhausted.
	Done bool
	// ScannedRows counts decoded rows read before filters were applied.
	ScannedRows int
	// ScannedHashSlots lists hash slots touched by the scan.
	ScannedHashSlots []HashSlot
}

// InspectTables returns the registered metadata table schemas for read-only discovery.
func InspectTables() []schema.Table {
	return Tables()
}

// InspectScan scans metadata rows for read-only inspection.
func InspectScan(ctx context.Context, db *MetaDB, req InspectScanRequest) (InspectScanResult, error) {
	if err := inspectCheckDB(db); err != nil {
		return InspectScanResult{}, err
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.HashSlotSet && req.After != nil && req.After.HashSlot != req.HashSlot {
		return InspectScanResult{}, fmt.Errorf("%w: inspect cursor hash slot mismatch", dberrors.ErrInvalidArgument)
	}
	slots, err := inspectScanSlots(req)
	if err != nil {
		return InspectScanResult{}, err
	}

	switch req.Table {
	case "user":
		return inspectScanTable(ctx, db, req, slots, userTable, inspectUserRow)
	case "device":
		return inspectScanTable(ctx, db, req, slots, deviceTable, inspectDeviceRow)
	case "channel":
		return inspectScanTable(ctx, db, req, slots, channelTable, inspectChannelRow)
	case "channel_runtime_meta":
		return inspectScanTable(ctx, db, req, slots, channelRuntimeMetaTable, inspectChannelRuntimeMetaRow)
	case "subscriber":
		return inspectScanTable(ctx, db, req, slots, subscriberTable, inspectSubscriberRow)
	case "conversation":
		return inspectScanTable(ctx, db, req, slots, conversationTable, inspectConversationRow)
	case "cmd_conversation":
		return inspectScanTable(ctx, db, req, slots, cmdConversationTable, inspectCMDConversationRow)
	case "plugin_binding":
		return inspectScanTable(ctx, db, req, slots, pluginBindingTable, inspectPluginBindingRow)
	case "channel_migration":
		return inspectScanTable(ctx, db, req, slots, channelMigrationTable, inspectChannelMigrationRow)
	case "hashslot_migration":
		return inspectScanTable(ctx, db, req, slots, hashSlotMigrationTable, inspectHashSlotMigrationRow)
	default:
		return InspectScanResult{}, fmt.Errorf("%w: unknown inspect table %q", dberrors.ErrInvalidArgument, req.Table)
	}
}

func inspectScanTable[R any](ctx context.Context, db *MetaDB, req InspectScanRequest, slots []HashSlot, table Table[R], rowFn func(R) InspectRow) (InspectScanResult, error) {
	afterPrimary, err := inspectCursorPrimary(req.Table, req.After, table.spec.Primary.Layout)
	if err != nil {
		return InspectScanResult{}, err
	}
	prefix := inspectPrimaryPrefix(req.Table, table, req.Filters)
	if req.After != nil && len(afterPrimary) > 0 && len(prefix) > 0 && !keyPartsHasPrefix(afterPrimary, prefix) {
		return InspectScanResult{}, fmt.Errorf("%w: inspect cursor outside primary prefix", dberrors.ErrInvalidArgument)
	}

	result := InspectScanResult{
		Rows: make([]InspectRow, 0, req.Limit),
		Done: true,
	}
	for _, slot := range slots {
		if len(result.Rows) >= req.Limit {
			result.Done = false
			result.Next = &InspectCursor{HashSlot: slot}
			break
		}
		var after KeyParts
		if req.After != nil && req.After.HashSlot == slot {
			after = afterPrimary
		}
		scannedSlot, done, next, err := inspectScanTableSlot(ctx, db.HashSlot(slot), table, prefix, after, req.Limit, req.Filters, rowFn, &result)
		if err != nil {
			return InspectScanResult{}, err
		}
		if scannedSlot {
			result.ScannedHashSlots = append(result.ScannedHashSlots, slot)
		}
		if !done {
			result.Done = false
			result.Next = &InspectCursor{HashSlot: slot, Primary: inspectCursorValues(next)}
			break
		}
	}
	return result, nil
}

func inspectScanSlots(req InspectScanRequest) ([]HashSlot, error) {
	if req.HashSlotSet {
		return []HashSlot{req.HashSlot}, nil
	}
	if req.HashSlotCount == 0 {
		return nil, fmt.Errorf("%w: hash slot count is required", dberrors.ErrInvalidArgument)
	}
	start := HashSlot(0)
	if req.After != nil {
		start = req.After.HashSlot
	}
	if start >= HashSlot(req.HashSlotCount) {
		return nil, nil
	}
	slots := make([]HashSlot, 0, int(req.HashSlotCount)-int(start))
	for slot := start; slot < HashSlot(req.HashSlotCount); slot++ {
		slots = append(slots, slot)
	}
	return slots, nil
}

func inspectCheckDB(db *MetaDB) error {
	if db == nil || db.engine == nil {
		return dberrors.ErrClosed
	}
	iter, err := db.engine.NewIter(engine.Span{}, engine.IterOptions{})
	if err != nil {
		return err
	}
	return iter.Close()
}

func inspectPrimaryPrefix[R any](tableName string, table Table[R], filters map[string]any) KeyParts {
	if len(filters) == 0 {
		return nil
	}
	columns := inspectColumnsByID(table.schema.Columns)
	prefix := make(KeyParts, 0, len(table.spec.Primary.Layout))
	for i, columnID := range table.spec.Primary.Columns {
		columnName, ok := columns[columnID]
		if !ok {
			break
		}
		value, ok := inspectPrimaryFilterValue(tableName, columnName, filters)
		if !ok {
			break
		}
		part, ok := inspectCoerceKeyPart(table.spec.Primary.Layout[i], value)
		if !ok {
			break
		}
		prefix = append(prefix, part)
	}
	return prefix
}

func inspectColumnsByID(columns []schema.Column) map[uint16]string {
	out := make(map[uint16]string, len(columns))
	for _, column := range columns {
		out[column.ID] = column.Name
	}
	return out
}

func inspectPrimaryFilterValue(tableName, columnName string, filters map[string]any) (any, bool) {
	for _, name := range inspectPrimaryFilterNames(tableName, columnName) {
		if value, ok := filters[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func inspectPrimaryFilterNames(tableName, columnName string) []string {
	if tableName == "user" && columnName == "key" {
		return []string{"uid", "key"}
	}
	return []string{columnName}
}

func inspectCursorPrimary(tableName string, cursor *InspectCursor, layout KeyLayout) (KeyParts, error) {
	if cursor == nil || len(cursor.Primary) == 0 {
		return nil, nil
	}
	if len(cursor.Primary) != len(layout) {
		return nil, fmt.Errorf("%w: invalid %s inspect cursor", dberrors.ErrInvalidArgument, tableName)
	}
	parts := make(KeyParts, 0, len(layout))
	for i, kind := range layout {
		value := cursor.Primary[i]
		switch kind {
		case KeyString, KeyInt64Ordered, KeyInt64Desc, KeyUint64, KeyUint8:
			part, ok := inspectCoerceKeyPart(kind, value)
			if !ok {
				return nil, fmt.Errorf("%w: invalid %s inspect cursor", dberrors.ErrInvalidArgument, tableName)
			}
			parts = append(parts, part)
		default:
			return nil, fmt.Errorf("%w: invalid %s inspect cursor", dberrors.ErrInvalidArgument, tableName)
		}
	}
	return parts, nil
}

func inspectCursorValues(parts KeyParts) []any {
	if len(parts) == 0 {
		return nil
	}
	values := make([]any, 0, len(parts))
	for _, part := range parts {
		switch part.Kind {
		case KeyString:
			values = append(values, part.S)
		case KeyInt64Ordered, KeyInt64Desc:
			values = append(values, part.I64)
		case KeyUint64:
			values = append(values, part.U64)
		case KeyUint8:
			values = append(values, part.U8)
		}
	}
	return values
}

func inspectCoerceKeyPart(kind KeyPartKind, value any) (KeyPart, bool) {
	switch kind {
	case KeyString:
		part, ok := value.(string)
		if !ok || part == "" {
			return KeyPart{}, false
		}
		return String(part), true
	case KeyInt64Ordered:
		part, ok := inspectCoerceInt64(value)
		if !ok {
			return KeyPart{}, false
		}
		return Int64Ordered(part), true
	case KeyInt64Desc:
		part, ok := inspectCoerceInt64(value)
		if !ok {
			return KeyPart{}, false
		}
		return Int64Desc(part), true
	case KeyUint64:
		part, ok := inspectCoerceUint64(value)
		if !ok {
			return KeyPart{}, false
		}
		return Uint64(part), true
	case KeyUint8:
		part, ok := inspectCoerceUint8(value)
		if !ok {
			return KeyPart{}, false
		}
		return Uint8(part), true
	default:
		return KeyPart{}, false
	}
}

func inspectCoerceInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		if uint64(v) > uint64(inspectMaxInt64) {
			return 0, false
		}
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > uint64(inspectMaxInt64) {
			return 0, false
		}
		return int64(v), true
	case float64:
		if !math.IsInf(v, 0) && !math.IsNaN(v) && math.Trunc(v) == v && v >= float64(inspectMinInt64) && v < 9223372036854775808.0 {
			return int64(v), true
		}
	}
	return 0, false
}

func inspectCoerceUint64(value any) (uint64, bool) {
	switch v := value.(type) {
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int8:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int16:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int32:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case uint:
		return uint64(v), true
	case uint8:
		return uint64(v), true
	case uint16:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case uint64:
		return v, true
	case float64:
		if !math.IsInf(v, 0) && !math.IsNaN(v) && math.Trunc(v) == v && v >= 0 && v < 18446744073709551616.0 {
			return uint64(v), true
		}
	}
	return 0, false
}

func inspectCoerceUint8(value any) (uint8, bool) {
	v, ok := inspectCoerceUint64(value)
	if !ok || v > 255 {
		return 0, false
	}
	return uint8(v), true
}

func inspectScanTableSlot[R any](ctx context.Context, shard *Shard, table Table[R], prefix KeyParts, after KeyParts, targetRows int, filters map[string]any, rowFn func(R) InspectRow, result *InspectScanResult) (bool, bool, KeyParts, error) {
	cursor := append(KeyParts(nil), after...)
	for len(result.Rows) < targetRows {
		pageLimit := targetRows - len(result.Rows)
		rows, next, done, err := inspectScanPrimaryPage(ctx, shard, table, prefix, cursor, pageLimit)
		if err != nil {
			return false, false, nil, err
		}
		result.ScannedRows += len(rows)
		for _, typedRow := range rows {
			row := rowFn(typedRow)
			if inspectRowMatches(row, filters) {
				result.Rows = append(result.Rows, row)
			}
		}
		if done {
			return true, true, nil, nil
		}
		if len(next) == 0 || next.Equal(cursor) {
			return true, false, nil, fmt.Errorf("%w: non-advancing %s inspect cursor", dberrors.ErrCorruptState, table.spec.Name)
		}
		cursor = append(KeyParts(nil), next...)
	}
	return true, false, cursor, nil
}

func inspectScanPrimaryPage[R any](ctx context.Context, shard *Shard, table Table[R], prefix KeyParts, cursor KeyParts, limit int) ([]R, KeyParts, bool, error) {
	if len(prefix) > 0 {
		return table.ScanPrimaryPrefix(ctx, shard, prefix, cursor, limit)
	}
	return table.ScanPrimary(ctx, shard, cursor, limit)
}

func inspectUserRow(user User) InspectRow {
	return InspectRow{
		"uid":          user.UID,
		"token":        user.Token,
		"device_flag":  user.DeviceFlag,
		"device_level": user.DeviceLevel,
	}
}

func inspectDeviceRow(device Device) InspectRow {
	return InspectRow{
		"uid":          device.UID,
		"device_flag":  device.DeviceFlag,
		"token":        device.Token,
		"device_level": device.DeviceLevel,
	}
}

func inspectChannelRow(channel Channel) InspectRow {
	return InspectRow{
		"channel_id":                  channel.ChannelID,
		"channel_type":                channel.ChannelType,
		"ban":                         channel.Ban,
		"disband":                     channel.Disband,
		"send_ban":                    channel.SendBan,
		"allow_stranger":              channel.AllowStranger,
		"subscriber_mutation_version": channel.SubscriberMutationVersion,
	}
}

func inspectChannelRuntimeMetaRow(meta ChannelRuntimeMeta) InspectRow {
	return InspectRow{
		"channel_id":              meta.ChannelID,
		"channel_type":            meta.ChannelType,
		"channel_epoch":           meta.ChannelEpoch,
		"leader_epoch":            meta.LeaderEpoch,
		"route_generation":        meta.RouteGeneration,
		"replicas":                meta.Replicas,
		"isr":                     meta.ISR,
		"leader":                  meta.Leader,
		"min_isr":                 meta.MinISR,
		"status":                  meta.Status,
		"features":                meta.Features,
		"lease_until_ms":          meta.LeaseUntilMS,
		"retention_through_seq":   meta.RetentionThroughSeq,
		"retention_updated_at_ms": meta.RetentionUpdatedAtMS,
		"write_fence_token":       meta.WriteFenceToken,
		"write_fence_version":     meta.WriteFenceVersion,
		"write_fence_reason":      meta.WriteFenceReason,
		"write_fence_until_ms":    meta.WriteFenceUntilMS,
	}
}

func inspectSubscriberRow(subscriber Subscriber) InspectRow {
	return InspectRow{
		"channel_id":   subscriber.ChannelID,
		"channel_type": subscriber.ChannelType,
		"uid":          subscriber.UID,
	}
}

func inspectConversationRow(state UserConversationState) InspectRow {
	return InspectRow{
		"uid":            state.UID,
		"channel_id":     state.ChannelID,
		"channel_type":   state.ChannelType,
		"read_seq":       state.ReadSeq,
		"deleted_to_seq": state.DeletedToSeq,
		"active_at":      state.ActiveAt,
		"updated_at":     state.UpdatedAt,
	}
}

func inspectCMDConversationRow(state CMDConversationState) InspectRow {
	return InspectRow{
		"uid":            state.UID,
		"channel_id":     state.ChannelID,
		"channel_type":   state.ChannelType,
		"read_seq":       state.ReadSeq,
		"deleted_to_seq": state.DeletedToSeq,
		"active_at":      state.ActiveAt,
		"updated_at":     state.UpdatedAt,
	}
}

func inspectPluginBindingRow(binding PluginUserBinding) InspectRow {
	return InspectRow{
		"uid":           binding.UID,
		"plugin_no":     binding.PluginNo,
		"created_at_ms": binding.CreatedAtMS,
		"updated_at_ms": binding.UpdatedAtMS,
	}
}

func inspectChannelMigrationRow(task ChannelMigrationTask) InspectRow {
	return InspectRow{
		"channel_id":           task.ChannelID,
		"channel_type":         task.ChannelType,
		"task_id":              task.TaskID,
		"kind":                 uint8(task.Kind),
		"status":               uint8(task.Status),
		"phase":                uint8(task.Phase),
		"source_node":          task.SourceNode,
		"target_node":          task.TargetNode,
		"desired_leader":       task.DesiredLeader,
		"owner_node_id":        task.OwnerNodeID,
		"owner_lease_until_ms": task.OwnerLeaseUntilMS,
		"created_at_ms":        task.CreatedAtMS,
		"updated_at_ms":        task.UpdatedAtMS,
		"completed_at_ms":      task.CompletedAtMS,
	}
}

func inspectHashSlotMigrationRow(state HashSlotMigrationState) InspectRow {
	return InspectRow{
		"hash_slot":         state.HashSlot,
		"source_slot":       state.SourceSlot,
		"target_slot":       state.TargetSlot,
		"phase":             state.Phase,
		"fence_index":       state.FenceIndex,
		"last_outbox_index": state.LastOutboxIndex,
		"last_acked_index":  state.LastAckedIndex,
	}
}

func inspectRowMatches(row InspectRow, filters map[string]any) bool {
	for key, want := range filters {
		got, ok := row[key]
		if !ok || !inspectValuesEqual(got, want) {
			return false
		}
	}
	return true
}

func inspectValuesEqual(got, want any) bool {
	gotNum, gotIsNumeric, gotOK := inspectIntegralNumeric(got)
	wantNum, wantIsNumeric, wantOK := inspectIntegralNumeric(want)
	if gotIsNumeric && wantIsNumeric {
		return gotOK && wantOK && gotNum == wantNum
	}
	return reflect.DeepEqual(got, want)
}

type inspectNumericValue struct {
	negative  bool
	magnitude uint64
}

func inspectIntegralNumeric(value any) (inspectNumericValue, bool, bool) {
	switch v := value.(type) {
	case int:
		return inspectSignedNumeric(int64(v)), true, true
	case int8:
		return inspectSignedNumeric(int64(v)), true, true
	case int16:
		return inspectSignedNumeric(int64(v)), true, true
	case int32:
		return inspectSignedNumeric(int64(v)), true, true
	case int64:
		return inspectSignedNumeric(v), true, true
	case uint:
		return inspectNumericValue{magnitude: uint64(v)}, true, true
	case uint8:
		return inspectNumericValue{magnitude: uint64(v)}, true, true
	case uint16:
		return inspectNumericValue{magnitude: uint64(v)}, true, true
	case uint32:
		return inspectNumericValue{magnitude: uint64(v)}, true, true
	case uint64:
		return inspectNumericValue{magnitude: v}, true, true
	case float32:
		return inspectFloatNumeric(float64(v))
	case float64:
		return inspectFloatNumeric(v)
	default:
		return inspectNumericValue{}, false, false
	}
}

func inspectSignedNumeric(value int64) inspectNumericValue {
	if value >= 0 {
		return inspectNumericValue{magnitude: uint64(value)}
	}
	return inspectNumericValue{negative: true, magnitude: uint64(-(value + 1)) + 1}
}

func inspectFloatNumeric(value float64) (inspectNumericValue, bool, bool) {
	if math.IsInf(value, 0) || math.IsNaN(value) || math.Trunc(value) != value {
		return inspectNumericValue{}, true, false
	}
	if value < 0 {
		if value < float64(inspectMinInt64) {
			return inspectNumericValue{}, true, false
		}
		return inspectSignedNumeric(int64(value)), true, true
	}
	if value >= 18446744073709551616.0 {
		return inspectNumericValue{}, true, false
	}
	return inspectNumericValue{magnitude: uint64(value)}, true, true
}
