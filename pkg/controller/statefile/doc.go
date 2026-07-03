// Package statefile provides atomic cluster-state.json load and save helpers.
//
// Stores read and write the complete durable state file using temporary files,
// fsync, and rename so callers either observe the previous valid snapshot or the
// next complete snapshot. Semantic validation remains in the state package and
// command application remains in the FSM.
package statefile
