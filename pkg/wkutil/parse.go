package wkutil

import "strconv"

func ParseInt(str string) int {
	limitI64, _ := strconv.ParseInt(str, 10, 64)
	return int(limitI64)
}

func ParseUint8(str string) uint8 {
	v, _ := strconv.ParseUint(str, 10, 8)
	return uint8(v)
}

func ParseInt64(str string) int64 {
	v, _ := strconv.ParseInt(str, 10, 64)
	return v
}

func ParseUint64(str string) uint64 {
	v, _ := strconv.ParseUint(str, 10, 64)
	return v
}
