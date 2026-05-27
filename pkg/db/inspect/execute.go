package inspect

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"

	db "github.com/WuKongIM/WuKongIM/pkg/db"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Query parses and executes one read-only inspect SQL statement.
func (s *Store) Query(ctx context.Context, raw string) (Result, error) {
	if s == nil {
		return Result{}, db.ErrInvalidArgument
	}
	q, err := Parse(raw)
	if err != nil {
		return Result{}, err
	}
	switch q.Kind {
	case QueryShowTables:
		return s.showTables(), nil
	case QueryDescribe:
		return s.describe(q.Table)
	case QuerySelect:
		p, err := planQuery(s.opts, q)
		if err != nil {
			return Result{}, err
		}
		return s.executePlan(ctx, p)
	default:
		return Result{}, ErrInvalidQuery
	}
}

func (s *Store) showTables() Result {
	rows := make([]Row, 0, len(metadb.InspectTables())+2)
	for _, table := range metadb.InspectTables() {
		rows = append(rows, Row{"domain": "meta", "name": table.Name, "table": "meta." + table.Name})
	}
	rows = append(rows,
		Row{"domain": "message", "name": "channels", "table": "message.channels"},
		Row{"domain": "message", "name": "message", "table": "message.message"},
	)
	sort.Slice(rows, func(i, j int) bool {
		return fmt.Sprint(rows[i]["table"]) < fmt.Sprint(rows[j]["table"])
	})
	return Result{Rows: rows, Stats: Stats{ReturnedRows: len(rows)}}
}

func (s *Store) describe(table string) (Result, error) {
	domain, name, ok := splitQualifiedTable(table)
	if !ok {
		return Result{}, ErrInvalidQuery
	}
	var rows []Row
	switch domain {
	case "meta":
		rows = describeInspectTable(domain, name)
	case "message":
		rows = describeInspectTable(domain, name)
	default:
		return Result{}, ErrInvalidQuery
	}
	if rows == nil {
		return Result{}, ErrInvalidQuery
	}
	return Result{Rows: rows, Stats: Stats{ReturnedRows: len(rows)}}, nil
}

func (s *Store) executePlan(ctx context.Context, p plan) (Result, error) {
	switch p.Domain {
	case "meta":
		return s.executeMeta(ctx, p)
	case "message":
		if p.ScanMode == scanModeMessageCatalog {
			return s.executeMessageCatalog(ctx, p)
		}
		return s.executeMessageChannel(ctx, p)
	default:
		return Result{}, ErrInvalidQuery
	}
}

func (s *Store) executeMeta(ctx context.Context, p plan) (Result, error) {
	if err := validateProjection(p.Domain, p.TableName, p.Query.Columns); err != nil {
		return Result{}, err
	}
	req := metadb.InspectScanRequest{
		Table:         p.TableName,
		HashSlot:      metadb.HashSlot(p.HashSlot),
		HashSlotSet:   p.HashSlotSet,
		HashSlotCount: p.HashSlotCount,
		Filters:       metaScanFilters(p.TableName, p.Query.Filters),
		Limit:         p.Query.Limit,
	}
	if p.Cursor != nil {
		req.After = &metadb.InspectCursor{
			HashSlot: metadb.HashSlot(p.Cursor.HashSlot),
			Primary:  p.Cursor.Primary,
		}
	}
	scan, err := metadb.InspectScan(ctx, s.metaDB, req)
	if err != nil {
		return Result{}, err
	}
	rows, err := projectRows(metaRowsToRows(scan.Rows), p.Domain, p.TableName, p.Query.Columns)
	if err != nil {
		return Result{}, err
	}
	stats := Stats{
		ScanMode:         p.ScanMode,
		ScannedHashSlots: metaHashSlotsToUint16(scan.ScannedHashSlots),
		ScannedRows:      scan.ScannedRows,
		ReturnedRows:     len(rows),
		HasMore:          !scan.Done,
	}
	if scan.Next != nil && !scan.Done {
		stats.NextCursor, err = encodeCursor(cursorPayload{
			Domain:    p.Domain,
			Table:     p.TableName,
			ScanMode:  p.ScanMode,
			HashSlot:  uint16(scan.Next.HashSlot),
			Primary:   scan.Next.Primary,
			QueryHash: queryHash(p.Query),
		})
		if err != nil {
			return Result{}, err
		}
	}
	return Result{Rows: rows, Stats: stats}, nil
}

func (s *Store) executeMessageCatalog(ctx context.Context, p plan) (Result, error) {
	if err := validateProjection(p.Domain, p.TableName, p.Query.Columns); err != nil {
		return Result{}, err
	}
	req := msgdb.InspectMessageRequest{Limit: p.Query.Limit}
	if value, ok := p.Query.Filters["channel_key"].(string); ok {
		req.ChannelKey = value
	}
	if p.Cursor != nil {
		req.AfterChannelKey = p.Cursor.ChannelKey
	}
	rows, scannedRows, next, done, err := s.scanFilteredMessageCatalog(ctx, req, p.Query.Filters, p.Query.Limit)
	if err != nil {
		return Result{}, err
	}
	rows, err = projectRows(rows, p.Domain, p.TableName, p.Query.Columns)
	if err != nil {
		return Result{}, err
	}
	stats := Stats{
		ScanMode:     p.ScanMode,
		ScannedRows:  scannedRows,
		ReturnedRows: len(rows),
		HasMore:      !done,
	}
	if next != nil && !done {
		stats.NextCursor, err = encodeCursor(cursorPayload{
			Domain:     p.Domain,
			Table:      p.TableName,
			ScanMode:   p.ScanMode,
			ChannelKey: next.AfterChannelKey,
			QueryHash:  queryHash(p.Query),
		})
		if err != nil {
			return Result{}, err
		}
	}
	return Result{Rows: rows, Stats: stats}, nil
}

func (s *Store) executeMessageChannel(ctx context.Context, p plan) (Result, error) {
	if err := validateProjection(p.Domain, p.TableName, p.Query.Columns); err != nil {
		return Result{}, err
	}
	channelKey := p.Query.Filters["channel_key"].(string)
	req := msgdb.InspectMessageRequest{ChannelKey: channelKey, Limit: p.Query.Limit}
	if p.Cursor != nil {
		req.AfterSeq = p.Cursor.AfterSeq
	}
	rows, scannedRows, next, done, err := s.scanFilteredMessageChannel(ctx, req, p.Query.Filters, p.Query.Limit)
	if err != nil {
		return Result{}, err
	}
	rows, err = projectRows(rows, p.Domain, p.TableName, p.Query.Columns)
	if err != nil {
		return Result{}, err
	}
	stats := Stats{
		ScanMode:     p.ScanMode,
		ScannedRows:  scannedRows,
		ReturnedRows: len(rows),
		HasMore:      !done,
	}
	if next != nil && !done {
		stats.NextCursor, err = encodeCursor(cursorPayload{
			Domain:     p.Domain,
			Table:      p.TableName,
			ScanMode:   p.ScanMode,
			ChannelKey: channelKey,
			AfterSeq:   next.AfterSeq,
			QueryHash:  queryHash(p.Query),
		})
		if err != nil {
			return Result{}, err
		}
	}
	return Result{Rows: rows, Stats: stats}, nil
}

func (s *Store) scanFilteredMessageCatalog(ctx context.Context, req msgdb.InspectMessageRequest, filters map[string]any, limit int) ([]Row, int, *msgdb.InspectMessageCursor, bool, error) {
	rows := make([]Row, 0, limit)
	scannedRows := 0
	for {
		req.Limit = limit - len(rows)
		scan, err := msgdb.InspectChannels(ctx, s.messageDB, req)
		if err != nil {
			return nil, 0, nil, false, err
		}
		scannedRows += scan.ScannedRows
		rows = appendBoundedRows(rows, filterRows(messageRowsToRows(scan.Rows), filters), limit)
		if len(rows) >= limit || scan.Done {
			return rows, scannedRows, scan.Next, scan.Done, nil
		}
		if scan.Next == nil || scan.Next.AfterChannelKey == "" {
			return rows, scannedRows, nil, true, nil
		}
		req.AfterChannelKey = scan.Next.AfterChannelKey
	}
}

func (s *Store) scanFilteredMessageChannel(ctx context.Context, req msgdb.InspectMessageRequest, filters map[string]any, limit int) ([]Row, int, *msgdb.InspectMessageCursor, bool, error) {
	rows := make([]Row, 0, limit)
	scannedRows := 0
	for {
		req.Limit = limit - len(rows)
		scan, err := msgdb.InspectMessages(ctx, s.messageDB, req)
		if err != nil {
			return nil, 0, nil, false, err
		}
		scannedRows += scan.ScannedRows
		pageRows := messageRowsToRows(scan.Rows)
		for _, row := range pageRows {
			row["channel_key"] = req.ChannelKey
		}
		rows = appendBoundedRows(rows, filterRows(pageRows, filters), limit)
		if len(rows) >= limit || scan.Done {
			return rows, scannedRows, scan.Next, scan.Done, nil
		}
		if scan.Next == nil {
			return rows, scannedRows, nil, true, nil
		}
		req.AfterSeq = scan.Next.AfterSeq
	}
}

func appendBoundedRows(dst []Row, src []Row, limit int) []Row {
	for _, row := range src {
		if len(dst) >= limit {
			return dst
		}
		dst = append(dst, row)
	}
	return dst
}

func describeInspectTable(domain, table string) []Row {
	columns, ok := inspectColumns[domain+"."+table]
	if !ok {
		return nil
	}
	rows := make([]Row, 0, len(columns))
	for _, column := range columns {
		rows = append(rows, Row{"column": column, "type": inspectColumnType(domain, table, column)})
	}
	return rows
}

func inspectColumnType(domain, table, column string) string {
	if typ, ok := inspectColumnTypes[domain+"."+table+"."+column]; ok {
		return typ
	}
	if typ, ok := inspectColumnTypes[column]; ok {
		return typ
	}
	return "any"
}

var inspectColumns = map[string][]string{
	"meta.user":                 {"uid", "token", "device_flag", "device_level"},
	"meta.device":               {"uid", "device_flag", "token", "device_level"},
	"meta.channel":              {"channel_id", "channel_type", "ban", "disband", "send_ban", "allow_stranger", "subscriber_mutation_version"},
	"meta.channel_runtime_meta": {"channel_id", "channel_type", "channel_epoch", "leader_epoch", "route_generation", "replicas", "isr", "leader", "min_isr", "status", "features", "lease_until_ms", "retention_through_seq", "retention_updated_at_ms", "write_fence_token", "write_fence_version", "write_fence_reason", "write_fence_until_ms"},
	"meta.subscriber":           {"channel_id", "channel_type", "uid"},
	"meta.conversation":         {"uid", "channel_id", "channel_type", "read_seq", "deleted_to_seq", "active_at", "updated_at"},
	"meta.cmd_conversation":     {"uid", "channel_id", "channel_type", "read_seq", "deleted_to_seq", "active_at", "updated_at"},
	"meta.plugin_binding":       {"uid", "plugin_no", "created_at_ms", "updated_at_ms"},
	"meta.channel_migration":    {"channel_id", "channel_type", "task_id", "kind", "status", "phase", "source_node", "target_node", "desired_leader", "owner_node_id", "owner_lease_until_ms", "created_at_ms", "updated_at_ms", "completed_at_ms"},
	"meta.hashslot_migration":   {"hash_slot", "source_slot", "target_slot", "phase", "fence_index", "last_outbox_index", "last_acked_index"},
	"message.channels":          {"channel_key", "channel_id", "channel_type"},
	"message.message":           {"channel_key", "message_seq", "message_id", "client_msg_no", "from_uid", "payload_hash", "payload_size", "payload"},
}

var inspectColumnTypes = map[string]string{
	"uid":                         "string",
	"token":                       "string",
	"device_flag":                 "int64",
	"device_level":                "int64",
	"channel_id":                  "string",
	"channel_type":                "uint8",
	"ban":                         "int64",
	"disband":                     "int64",
	"send_ban":                    "int64",
	"allow_stranger":              "int64",
	"subscriber_mutation_version": "uint64",
	"channel_epoch":               "uint64",
	"leader_epoch":                "uint64",
	"route_generation":            "uint64",
	"replicas":                    "uint64_array",
	"isr":                         "uint64_array",
	"leader":                      "uint64",
	"min_isr":                     "uint8",
	"status":                      "uint8",
	"features":                    "string_array",
	"lease_until_ms":              "int64",
	"retention_through_seq":       "uint64",
	"retention_updated_at_ms":     "int64",
	"write_fence_token":           "string",
	"write_fence_version":         "uint64",
	"write_fence_reason":          "string",
	"write_fence_until_ms":        "int64",
	"read_seq":                    "uint64",
	"deleted_to_seq":              "uint64",
	"active_at":                   "int64",
	"updated_at":                  "int64",
	"plugin_no":                   "string",
	"created_at_ms":               "int64",
	"updated_at_ms":               "int64",
	"task_id":                     "string",
	"kind":                        "uint8",
	"phase":                       "uint8",
	"source_node":                 "uint64",
	"target_node":                 "uint64",
	"desired_leader":              "uint64",
	"owner_node_id":               "uint64",
	"owner_lease_until_ms":        "int64",
	"completed_at_ms":             "int64",
	"hash_slot":                   "uint16",
	"source_slot":                 "uint64",
	"target_slot":                 "uint64",
	"fence_index":                 "uint64",
	"last_outbox_index":           "uint64",
	"last_acked_index":            "uint64",
	"channel_key":                 "string",
	"message_seq":                 "uint64",
	"message_id":                  "uint64",
	"client_msg_no":               "string",
	"from_uid":                    "string",
	"payload_hash":                "uint64",
	"payload_size":                "uint64",
	"payload":                     "bytes",
}

func validateProjection(domain, table string, columns []string) error {
	tableColumns, ok := inspectColumns[domain+"."+table]
	if !ok {
		return ErrInvalidQuery
	}
	if len(columns) == 0 || len(columns) == 1 && columns[0] == "*" {
		return nil
	}
	allowed := make(map[string]struct{}, len(tableColumns))
	for _, column := range tableColumns {
		allowed[column] = struct{}{}
	}
	for _, column := range columns {
		if _, ok := allowed[column]; !ok {
			return fmt.Errorf("%w: unknown column %q", ErrInvalidQuery, column)
		}
	}
	return nil
}

func projectRows(rows []Row, domain, table string, columns []string) ([]Row, error) {
	if err := validateProjection(domain, table, columns); err != nil {
		return nil, err
	}
	if len(columns) == 0 || len(columns) == 1 && columns[0] == "*" {
		return rows, nil
	}
	projected := make([]Row, 0, len(rows))
	for _, row := range rows {
		out := make(Row, len(columns))
		for _, column := range columns {
			if value, ok := row[column]; ok {
				out[column] = value
			}
		}
		projected = append(projected, out)
	}
	return projected, nil
}

func metaScanFilters(table string, filters map[string]any) map[string]any {
	if len(filters) == 0 {
		return nil
	}
	out := make(map[string]any, len(filters))
	for key, value := range filters {
		if key != "hash_slot" || table == "hashslot_migration" {
			out[key] = value
		}
	}
	return out
}

func filterRows(rows []Row, filters map[string]any) []Row {
	if len(filters) == 0 || len(rows) == 0 {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		if rowMatchesFilters(row, filters) {
			out = append(out, row)
		}
	}
	return out
}

func rowMatchesFilters(row Row, filters map[string]any) bool {
	for key, want := range filters {
		got, ok := row[key]
		if !ok || !valuesEqual(got, want) {
			return false
		}
	}
	return true
}

func valuesEqual(got, want any) bool {
	gotNum, gotNumeric, gotOK := integralNumeric(got)
	wantNum, wantNumeric, wantOK := integralNumeric(want)
	if gotNumeric && wantNumeric {
		return gotOK && wantOK && gotNum == wantNum
	}
	return reflect.DeepEqual(got, want)
}

type numericValue struct {
	negative  bool
	magnitude uint64
}

func integralNumeric(value any) (numericValue, bool, bool) {
	switch v := value.(type) {
	case int:
		return signedNumeric(int64(v)), true, true
	case int8:
		return signedNumeric(int64(v)), true, true
	case int16:
		return signedNumeric(int64(v)), true, true
	case int32:
		return signedNumeric(int64(v)), true, true
	case int64:
		return signedNumeric(v), true, true
	case uint:
		return numericValue{magnitude: uint64(v)}, true, true
	case uint8:
		return numericValue{magnitude: uint64(v)}, true, true
	case uint16:
		return numericValue{magnitude: uint64(v)}, true, true
	case uint32:
		return numericValue{magnitude: uint64(v)}, true, true
	case uint64:
		return numericValue{magnitude: v}, true, true
	case float32:
		return floatNumeric(float64(v))
	case float64:
		return floatNumeric(v)
	default:
		return numericValue{}, false, false
	}
}

func signedNumeric(value int64) numericValue {
	if value >= 0 {
		return numericValue{magnitude: uint64(value)}
	}
	return numericValue{negative: true, magnitude: uint64(-(value + 1)) + 1}
}

func floatNumeric(value float64) (numericValue, bool, bool) {
	if math.IsInf(value, 0) || math.IsNaN(value) || math.Trunc(value) != value {
		return numericValue{}, true, false
	}
	if value < 0 {
		if value < float64(math.MinInt64) {
			return numericValue{}, true, false
		}
		return signedNumeric(int64(value)), true, true
	}
	if value >= 18446744073709551616.0 {
		return numericValue{}, true, false
	}
	return numericValue{magnitude: uint64(value)}, true, true
}

func metaRowsToRows(rows []metadb.InspectRow) []Row {
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		out = append(out, cloneRow(row))
	}
	return out
}

func messageRowsToRows(rows []msgdb.InspectMessageRow) []Row {
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		out = append(out, cloneRow(row))
	}
	return out
}

func cloneRow[M ~map[string]any](row M) Row {
	out := make(Row, len(row))
	for key, value := range row {
		out[key] = value
	}
	return out
}

func metaHashSlotsToUint16(slots []metadb.HashSlot) []uint16 {
	out := make([]uint16, 0, len(slots))
	for _, slot := range slots {
		out = append(out, uint16(slot))
	}
	return out
}
