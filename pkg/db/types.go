package db

// Durability describes how a write is synchronized to durable storage.
type Durability uint8

const (
	// NoSync writes through Pebble without waiting for an fsync.
	NoSync Durability = iota
	// Sync commits one batch and waits for an fsync.
	Sync
	// CoordinatedSync lets a domain coordinator share one fsync across requests.
	CoordinatedSync
)

// Domain identifies a logical storage domain in the unified keyspace.
type Domain uint8

const (
	// DomainMessage stores channel message log data.
	DomainMessage Domain = 0x01
	// DomainMeta stores hash-slot metadata data.
	DomainMeta Domain = 0x02
)
