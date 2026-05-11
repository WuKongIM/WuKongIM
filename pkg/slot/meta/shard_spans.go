package meta

import "encoding/binary"

type Span struct {
	Start []byte
	End   []byte
}

func hashSlotStateSpan(hashSlot uint16) Span {
	return newPrefixSpan(encodeHashSlotKeyspacePrefix(keyspaceState, hashSlot))
}

func hashSlotIndexSpan(hashSlot uint16) Span {
	return newPrefixSpan(encodeHashSlotKeyspacePrefix(keyspaceIndex, hashSlot))
}

func hashSlotMetaSpan(hashSlot uint16) Span {
	return newPrefixSpan(encodeHashSlotKeyspacePrefix(keyspaceMeta, hashSlot))
}

func hashSlotAllDataSpans(hashSlot uint16) []Span {
	return []Span{
		hashSlotStateSpan(hashSlot),
		hashSlotIndexSpan(hashSlot),
		hashSlotMetaSpan(hashSlot),
	}
}

func encodeHashSlotKeyspacePrefix(kind byte, hashSlot uint16) []byte {
	prefix := make([]byte, 0, 1+2)
	prefix = append(prefix, kind)
	prefix = binary.BigEndian.AppendUint16(prefix, hashSlot)
	return prefix
}

func newPrefixSpan(prefix []byte) Span {
	return Span{
		Start: append([]byte(nil), prefix...),
		End:   nextPrefix(prefix),
	}
}

func nextPrefix(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] == 0xff {
			continue
		}
		end[i]++
		return end[:i+1]
	}
	return []byte{0xff}
}
