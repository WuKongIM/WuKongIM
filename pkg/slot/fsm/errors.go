package fsm

import "errors"

// ErrHashSlotFenced reports that the source side of a hash-slot migration is fenced.
var ErrHashSlotFenced = errors.New("fsm: hash slot fenced")
