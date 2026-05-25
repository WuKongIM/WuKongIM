package rowcodec

import "encoding/binary"

func encodeZigZagInt64(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

func decodeZigZagInt64(value uint64) int64 {
	return int64(value>>1) ^ -int64(value&1)
}

func appendUvarint(dst []byte, value uint64) []byte {
	return binary.AppendUvarint(dst, value)
}

func readUvarint(src []byte) (uint64, []byte, bool) {
	value, n := binary.Uvarint(src)
	if n <= 0 {
		return 0, nil, false
	}
	return value, src[n:], true
}
