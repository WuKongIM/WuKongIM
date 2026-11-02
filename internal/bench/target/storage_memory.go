package target

import (
	"math"
	"math/big"
	"strconv"
)

// StorageMemoryMetrics contains optional byte gauges, not cache capacities or RSS.
// Producer age and reader-pinned blocks outside the cache are not measured here.
type StorageMemoryMetrics struct {
	BlockCacheBytes       uint64 `json:"block_cache_bytes"`
	MemTableReservedBytes uint64 `json:"memtable_reserved_bytes"`
	BlockCacheKnown       bool   `json:"block_cache_known"`
	MemTableReservedKnown bool   `json:"memtable_reserved_known"`
}

// observeStorageMemoryMetric handles only two additive diagnostic families.
// Optional malformed evidence is discarded without changing the original parser outcome.
func (s *MetricsSnapshot) observeStorageMemoryMetric(name, line string) bool {
	var family int
	switch name {
	case "wukongim_storage_pebble_block_cache_size_bytes":
		family = 0
	case "wukongim_storage_pebble_memtable_reserved_size_bytes":
		family = 1
	default:
		return false
	}
	token, text, err := splitObservationMetricSample(line)
	if err != nil {
		s.invalidateStorageMemoryFamily(family)
		return true
	}
	_, labels, err := parseObservationMetricToken(token)
	if err != nil {
		s.invalidateStorageMemoryFamily(family)
		return true
	}
	var store int
	switch labels["store"] {
	case "channel_log":
		store = 0
	case "meta":
		store = 1
	case "raft":
		store = 2
	default:
		return true
	}
	value, valid := exactStorageMemoryBytes(text)
	for label := range labels {
		if label != "store" && label != "node_id" && label != "node_name" {
			valid = false
		}
	}
	bit := uint8(1 << store)
	if s.storageMemorySeen[family]&bit != 0 {
		valid = false
	}
	s.storageMemorySeen[family] |= bit
	if !valid {
		value = 0
	}
	s.setStorageMemoryValue(family, store, value, valid)
	return true
}

// exactStorageMemoryBytes rejects fractions without rounding a long decimal into
// a trustworthy integer. Precision exceeds the entire bounded decimal significand.
func exactStorageMemoryBytes(text string) (uint64, bool) {
	if len(text) == 0 || len(text) > 128 {
		return 0, false
	}
	// Reject huge exponents before any arbitrary-precision work. This rounded
	// value is only a cheap range filter; exact acceptance still happens below.
	approximate, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(approximate) || math.IsInf(approximate, 0) || approximate < 0 || approximate > 1<<53 {
		return 0, false
	}
	if approximate == 0 {
		// A nonzero mantissa may have underflowed. Only literal decimal zero
		// is trustworthy, and it needs no exponent expansion at all.
		for _, c := range text {
			if c == 'e' || c == 'E' {
				break
			}
			if c != '0' && c != '.' && c != '+' && c != '-' {
				return 0, false
			}
		}
		return 0, true
	}
	parsed, _, err := big.ParseFloat(text, 10, uint(len(text)*4+64), big.ToNearestEven)
	if err != nil || parsed.Sign() < 0 {
		return 0, false
	}
	integer, accuracy := parsed.Int(nil)
	if accuracy != big.Exact || integer == nil || integer.Sign() < 0 || integer.BitLen() > 54 {
		return 0, false
	}
	value := integer.Uint64()
	return value, value <= uint64(1)<<53
}

func (s *MetricsSnapshot) invalidateStorageMemoryFamily(family int) {
	// With an unparseable label set, no store in this family can be trusted.
	s.storageMemorySeen[family] = 0b111
	for store := range s.StorageMemory {
		s.setStorageMemoryValue(family, store, 0, false)
	}
}

func (s *MetricsSnapshot) setStorageMemoryValue(family, store int, value uint64, known bool) {
	if family == 0 {
		s.StorageMemory[store].BlockCacheBytes = value
		s.StorageMemory[store].BlockCacheKnown = known
	} else {
		s.StorageMemory[store].MemTableReservedBytes = value
		s.StorageMemory[store].MemTableReservedKnown = known
	}
}
