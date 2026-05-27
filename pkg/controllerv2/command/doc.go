// Package command defines versioned ControllerV2 Raft command envelopes.
//
// Commands use a stable versioned envelope and JSON codec so Raft log entries
// can remain durable while individual command schemas evolve. The package owns
// command names, JSON encode/decode helpers, and typed payload structs shared by
// proposers and the FSM.
package command
