package errors

import "errors"

var (
	TagNotExist = func(tagKey string) error {
		return errors.New("tag not exist: " + tagKey)
	}
	TagSlotLeaderIsZero = errors.New("tag slot leader is 0")
)
