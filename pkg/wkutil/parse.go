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

func ParseUint32(str string) uint32 {
	v, _ := strconv.ParseUint(str, 10, 32)
	return uint32(v)
}

func ParseFloat64(str string) float64 {
	v, _ := strconv.ParseFloat(str, 64)
	return v
}

func ParseBool(str string) bool {
	if str == "" {
		return false
	}
	v, _ := strconv.ParseBool(str)
	return v
}

func Uint64ToString(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func Int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
