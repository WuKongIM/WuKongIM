package quorumlog

// AppendOutcome classifies what storage can prove about one immutable append.
// The zero value is invalid so callers cannot accidentally treat an omitted
// classification as a durable result.
type AppendOutcome uint8

const (
	AppendOutcomeUnspecified AppendOutcome = iota
	// AppendOutcomeDurable proves this call atomically persisted the request.
	AppendOutcomeDurable
	// AppendOutcomeAlreadyDurable proves an exact immutable retry was present.
	AppendOutcomeAlreadyDurable
	// AppendOutcomeDefinitelyNotWritten proves no part of the request committed.
	AppendOutcomeDefinitelyNotWritten
	// AppendOutcomeConflict proves durable state disagrees with the request.
	AppendOutcomeConflict
	// AppendOutcomeUnknown means the caller lost certainty after commit admission.
	AppendOutcomeUnknown
)

// Valid reports whether the outcome is one of the closed append classifications.
func (o AppendOutcome) Valid() bool {
	return o >= AppendOutcomeDurable && o <= AppendOutcomeUnknown
}

// Durable reports whether storage proved the exact request is durable.
func (o AppendOutcome) Durable() bool {
	return o == AppendOutcomeDurable || o == AppendOutcomeAlreadyDurable
}
