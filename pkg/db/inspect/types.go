package inspect

import "errors"

var (
	// ErrInvalidQuery reports malformed inspect SQL.
	ErrInvalidQuery = errors.New("inspect: invalid query")
	// ErrUnsupportedQuery reports valid SQL outside the inspect subset.
	ErrUnsupportedQuery = errors.New("inspect: unsupported query")
	// ErrCursorMismatch reports a cursor that does not match the requested scan.
	ErrCursorMismatch = errors.New("inspect: cursor mismatch")
	// ErrHashSlotRequired reports a metadata scan that needs a hash slot count.
	ErrHashSlotRequired = errors.New("inspect: hash slot required")
)

// QueryKind identifies the inspect query operation.
type QueryKind uint8

const (
	// QueryShowTables lists inspectable tables.
	QueryShowTables QueryKind = iota + 1
	// QueryDescribe describes one inspectable table.
	QueryDescribe
	// QuerySelect scans rows from one inspectable table.
	QuerySelect
)

// Options configures a read-only inspect store.
type Options struct {
	// MetaPath is the Pebble path for metadata storage.
	MetaPath string
	// MessagePath is the Pebble path for message storage.
	MessagePath string
	// HashSlotCount bounds metadata scans across hash slots.
	HashSlotCount uint16
	// DefaultLimit is used when a query omits LIMIT.
	DefaultLimit int
	// MaxLimit caps query LIMIT values.
	MaxLimit int
}

// Query is the parsed form of an inspect SQL statement.
type Query struct {
	// Kind identifies the query operation.
	Kind QueryKind
	// Table is the inspect table name.
	Table string
	// Columns lists requested result columns.
	Columns []string
	// Filters maps equality filter columns to literal values.
	Filters map[string]any
	// Limit bounds returned rows when positive.
	Limit int
	// Cursor resumes a previous scan.
	Cursor string
}

// Row contains one inspect result row.
type Row map[string]any

// Result contains rows and scan metadata for an inspect query.
type Result struct {
	// Rows contains returned inspect rows.
	Rows []Row
	// Stats summarizes scan work and pagination state.
	Stats Stats
}

// Stats summarizes inspect query execution.
type Stats struct {
	// ScanMode names the scan strategy used.
	ScanMode string
	// ScannedHashSlots lists metadata hash slots touched.
	ScannedHashSlots []uint16
	// ScannedRows counts decoded rows considered.
	ScannedRows int
	// ReturnedRows counts rows returned to the caller.
	ReturnedRows int
	// HasMore reports whether another page is available.
	HasMore bool
	// NextCursor resumes the next page when HasMore is true.
	NextCursor string
}
