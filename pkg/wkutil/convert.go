package wkutil

import "strconv"

func StringToUint8(s string) uint8 {
	i, _ := strconv.ParseUint(s, 10, 8)
	return uint8(i)
}
