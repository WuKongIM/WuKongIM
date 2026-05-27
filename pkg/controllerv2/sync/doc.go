// Package sync provides full-file leader state sync for non-controller nodes.
//
// Controller voters serve complete cluster-state snapshots, and mirror nodes
// fetch those files from the current leader instead of joining Controller Raft.
// The protocol supports redirects, not-ready responses, and not-modified checks
// while preserving the leader-owned durable state semantics.
package sync
