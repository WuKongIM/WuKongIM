package schema

// Type describes the logical value type of a column.
type Type uint8

const (
	// TypeString stores a string column.
	TypeString Type = iota + 1
	// TypeBytes stores an arbitrary byte slice column.
	TypeBytes
	// TypeInt64 stores a signed 64-bit integer column.
	TypeInt64
	// TypeUint64 stores an unsigned 64-bit integer column.
	TypeUint64
	// TypeBool stores a bool column.
	TypeBool
	// TypeUint8 stores an unsigned 8-bit integer column.
	TypeUint8
)

// Table describes one durable table schema.
type Table struct {
	// ID is the stable table identifier encoded into keys.
	ID uint32
	// Name is a human-readable table name used by tests and diagnostics.
	Name string
	// Columns lists every logical column defined by this table.
	Columns []Column
	// Families groups non-primary-key columns into row value families.
	Families []Family
	// Primary describes the primary key index.
	Primary Index
	// Indexes lists secondary indexes.
	Indexes []Index
}

// Column describes one logical table column.
type Column struct {
	// ID is the stable column identifier encoded into values and indexes.
	ID uint16
	// Name is a human-readable column name.
	Name string
	// Type is the encoded value type.
	Type Type
	// Required reports whether the column must be present when decoding rows.
	Required bool
}

// Family describes one row value family.
type Family struct {
	// ID is the stable family identifier encoded into row keys.
	ID uint16
	// Name is a human-readable family name.
	Name string
	// Columns lists encoded columns in this family.
	Columns []uint16
}

// Index describes a primary or secondary index.
type Index struct {
	// ID is the stable index identifier encoded into index keys.
	ID uint16
	// Name is a human-readable index name.
	Name string
	// Unique reports whether the index has at most one row per key.
	Unique bool
	// Primary reports whether this is the primary key.
	Primary bool
	// Columns lists key columns in encoded order.
	Columns []uint16
	// Covering lists optional columns stored in the index value.
	Covering []uint16
}

// Family returns the family descriptor with id.
func (t Table) Family(id uint16) (Family, bool) {
	for _, family := range t.Families {
		if family.ID == id {
			return family, true
		}
	}
	return Family{}, false
}
