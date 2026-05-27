// Package command defines versioned ControllerV2 Raft command envelopes.
//
// Commands use a stable outer envelope and versioned payload codecs so Raft log
// entries can remain durable while individual command schemas evolve. The
// package owns command names, encode/decode helpers, and typed payload structs
// shared by proposers and the FSM.
package command
