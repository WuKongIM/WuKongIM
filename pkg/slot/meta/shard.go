package meta

import "math"

type ShardStore struct {
	db      *DB
	slot    uint16
	rawSlot uint64
}

func (s *ShardStore) validate() error {
	if s == nil || s.db == nil {
		return ErrInvalidArgument
	}
	return validateSlot(s.rawSlot)
}

func validateSlot(slot uint64) error {
	if slot > math.MaxUint16 {
		return ErrInvalidArgument
	}
	return nil
}

func validateHashSlot(hashSlot uint16) error {
	_ = hashSlot
	return nil
}
