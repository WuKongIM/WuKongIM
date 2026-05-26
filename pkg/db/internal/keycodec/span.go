package keycodec

// Span identifies an ordered half-open key range [Start, End).
type Span struct {
	// Start is the inclusive lower bound.
	Start []byte
	// End is the exclusive upper bound.
	End []byte
}

// NewPrefixSpan returns the half-open range containing all keys with prefix.
func NewPrefixSpan(prefix []byte) Span {
	return Span{Start: append([]byte(nil), prefix...), End: PrefixEnd(prefix)}
}

// PrefixEnd returns the smallest key that sorts after all keys with prefix.
func PrefixEnd(prefix []byte) []byte {
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
