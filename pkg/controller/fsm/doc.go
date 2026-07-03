// Package fsm applies committed ControllerV2 commands to durable cluster state.
//
// The state machine owns semantic command application. It loads the current
// cluster-state.json snapshot, applies committed Raft entries deterministically,
// saves the final state once per batch, and publishes an in-memory snapshot only
// after durable save succeeds.
package fsm
