package wkutil

import "hash/crc32"

// HashCrc32 通过字符串获取32位数字
func HashCrc32(str string) uint32 {

	return crc32.ChecksumIEEE([]byte(str))
}
