package meta

import "encoding/binary"

type Span struct {
	Start []byte
	End   []byte
}

func slotStateSpan(slot uint64) Span {
	return hashSlotStateSpan(uint16(slot))
}

func slotIndexSpan(slot uint64) Span {
	return hashSlotIndexSpan(uint16(slot))
}

func slotMetaSpan(slot uint64) Span {
	return hashSlotMetaSpan(uint16(slot))
}

func slotAllDataSpans(slot uint64) []Span {
	return hashSlotAllDataSpans(uint16(slot))
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

func encodeSlotKeyspacePrefix(kind byte, slot uint64) []byte {
	return encodeHashSlotKeyspacePrefix(kind, uint16(slot))
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
