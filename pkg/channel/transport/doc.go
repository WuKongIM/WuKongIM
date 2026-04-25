// Package transport provides the node-transport-backed adapter for the channel
// data plane. Steady-state replication is driven by fixed `(peer,lane)`
// LongPollFetch RPCs that park on the leader and reissue from the follower.
// ReconcileProbe stays separate from the steady-state path, and the plain
// Fetch RPC remains available as an auxiliary transport helper.
package transport
