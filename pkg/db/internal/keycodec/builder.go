package keycodec

// Builder incrementally constructs ordered storage keys with scratch reuse.
type Builder struct {
	buf []byte
}

// Reset clears the builder and returns it for chained calls.
func (b *Builder) Reset() *Builder {
	b.buf = b.buf[:0]
	return b
}

// Domain appends the top-level domain byte.
func (b *Builder) Domain(domain Domain) *Builder {
	b.buf = append(b.buf, byte(domain))
	return b
}

// Partition appends a partition kind and opaque partition identifier.
func (b *Builder) Partition(kind PartitionKind, id []byte) *Builder {
	b.buf = append(b.buf, byte(kind))
	b.buf = append(b.buf, id...)
	return b
}

// Row appends the row space and table ID.
func (b *Builder) Row(tableID uint32) *Builder {
	b.buf = append(b.buf, byte(SpaceRow))
	b.buf = AppendUint32(b.buf, tableID)
	return b
}

// Index appends the index space, table ID, and index ID.
func (b *Builder) Index(tableID uint32, indexID uint16) *Builder {
	b.buf = append(b.buf, byte(SpaceIndex))
	b.buf = AppendUint32(b.buf, tableID)
	b.buf = AppendUint16(b.buf, indexID)
	return b
}

// System appends the system space, table ID, and system ID.
func (b *Builder) System(tableID uint32, systemID uint16) *Builder {
	b.buf = append(b.buf, byte(SpaceSystem))
	b.buf = AppendUint32(b.buf, tableID)
	b.buf = AppendUint16(b.buf, systemID)
	return b
}

// Catalog appends the catalog space.
func (b *Builder) Catalog() *Builder {
	b.buf = append(b.buf, byte(SpaceCatalog))
	return b
}

// String appends a string key part.
func (b *Builder) String(value string) *Builder {
	b.buf = AppendString(b.buf, value)
	return b
}

// Uint64 appends a uint64 key part.
func (b *Builder) Uint64(value uint64) *Builder {
	b.buf = AppendUint64(b.buf, value)
	return b
}

// Int64Ordered appends an int64 key part that sorts by numeric order.
func (b *Builder) Int64Ordered(value int64) *Builder {
	b.buf = AppendInt64Ordered(b.buf, value)
	return b
}

// Int64Desc appends an int64 key part that sorts by descending numeric order.
func (b *Builder) Int64Desc(value int64) *Builder {
	b.buf = AppendInt64Desc(b.buf, value)
	return b
}

// Family appends a row family ID.
func (b *Builder) Family(familyID uint16) *Builder {
	b.buf = AppendUint16(b.buf, familyID)
	return b
}

// Key returns a copy of the constructed key.
func (b *Builder) Key() []byte {
	return append([]byte(nil), b.buf...)
}

// Bytes returns the builder scratch bytes. Callers must copy before reuse.
func (b *Builder) Bytes() []byte {
	return b.buf
}
