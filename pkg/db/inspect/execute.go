package inspect

import (
	"context"
	"fmt"
	"sort"

	db "github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
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
		for _, metaTable := range metadb.InspectTables() {
			if metaTable.Name == name {
				rows = describeSchemaColumns(metaTable.Columns)
				break
			}
		}
	case "message":
		rows = describeMessageTable(name)
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
		Filters:       filtersWithoutHashSlot(p.Query.Filters),
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
	scan, err := msgdb.InspectChannels(ctx, s.messageDB, req)
	if err != nil {
		return Result{}, err
	}
	rows, err := projectRows(messageRowsToRows(scan.Rows), p.Domain, p.TableName, p.Query.Columns)
	if err != nil {
		return Result{}, err
	}
	stats := Stats{
		ScanMode:     p.ScanMode,
		ScannedRows:  scan.ScannedRows,
		ReturnedRows: len(rows),
		HasMore:      !scan.Done,
	}
	if scan.Next != nil && !scan.Done {
		stats.NextCursor, err = encodeCursor(cursorPayload{
			Domain:     p.Domain,
			Table:      p.TableName,
			ScanMode:   p.ScanMode,
			ChannelKey: scan.Next.AfterChannelKey,
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
	scan, err := msgdb.InspectMessages(ctx, s.messageDB, req)
	if err != nil {
		return Result{}, err
	}
	rows := messageRowsToRows(scan.Rows)
	for _, row := range rows {
		row["channel_key"] = channelKey
	}
	rows, err = projectRows(rows, p.Domain, p.TableName, p.Query.Columns)
	if err != nil {
		return Result{}, err
	}
	stats := Stats{
		ScanMode:     p.ScanMode,
		ScannedRows:  scan.ScannedRows,
		ReturnedRows: len(rows),
		HasMore:      !scan.Done,
	}
	if scan.Next != nil && !scan.Done {
		stats.NextCursor, err = encodeCursor(cursorPayload{
			Domain:     p.Domain,
			Table:      p.TableName,
			ScanMode:   p.ScanMode,
			ChannelKey: channelKey,
			AfterSeq:   scan.Next.AfterSeq,
			QueryHash:  queryHash(p.Query),
		})
		if err != nil {
			return Result{}, err
		}
	}
	return Result{Rows: rows, Stats: stats}, nil
}

func describeSchemaColumns(columns []schema.Column) []Row {
	rows := make([]Row, 0, len(columns))
	for _, column := range columns {
		rows = append(rows, Row{
			"column":   column.Name,
			"type":     schemaTypeName(column.Type),
			"required": column.Required,
		})
	}
	return rows
}

func describeMessageTable(table string) []Row {
	columns, ok := inspectColumns["message."+table]
	if !ok {
		return nil
	}
	rows := make([]Row, 0, len(columns))
	for _, column := range columns {
		rows = append(rows, Row{"column": column, "type": messageColumnTypes[column]})
	}
	return rows
}

func schemaTypeName(t schema.Type) string {
	switch t {
	case schema.TypeString:
		return "string"
	case schema.TypeBytes:
		return "bytes"
	case schema.TypeInt64:
		return "int64"
	case schema.TypeUint64:
		return "uint64"
	case schema.TypeBool:
		return "bool"
	case schema.TypeUint8:
		return "uint8"
	default:
		return "unknown"
	}
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

var messageColumnTypes = map[string]string{
	"channel_key":   "string",
	"channel_id":    "string",
	"channel_type":  "uint8",
	"message_seq":   "uint64",
	"message_id":    "uint64",
	"client_msg_no": "string",
	"from_uid":      "string",
	"payload_hash":  "uint64",
	"payload_size":  "uint64",
	"payload":       "bytes",
}

func validateProjection(domain, table string, columns []string) error {
	if len(columns) == 0 || len(columns) == 1 && columns[0] == "*" {
		return nil
	}
	allowed := make(map[string]struct{}, len(inspectColumns[domain+"."+table]))
	for _, column := range inspectColumns[domain+"."+table] {
		allowed[column] = struct{}{}
	}
	if len(allowed) == 0 {
		return ErrInvalidQuery
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

func filtersWithoutHashSlot(filters map[string]any) map[string]any {
	if len(filters) == 0 {
		return nil
	}
	out := make(map[string]any, len(filters))
	for key, value := range filters {
		if key != "hash_slot" {
			out[key] = value
		}
	}
	return out
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
