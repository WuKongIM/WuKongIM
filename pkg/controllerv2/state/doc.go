// Package state defines the durable ControllerV2 cluster-state model.
//
// The model is the canonical cluster-state.json schema. This package owns
// normalization, validation, checksums, and initial hash-slot construction while
// preserving durable JSON tags and deterministic state hashing across releases.
package state
