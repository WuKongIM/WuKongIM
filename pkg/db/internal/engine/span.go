package engine

// Span identifies an ordered half-open key range [Start, End).
type Span struct {
	// Start is the inclusive lower bound.
	Start []byte
	// End is the exclusive upper bound. Empty means unbounded.
	End []byte
}

// IterOptions configures iterator behavior.
type IterOptions struct {
	// Reverse is reserved for future reverse-scan helpers.
	Reverse bool
}
