// Package channel is a multiple-reactor channel log runtime.
//
// It validates append, follower apply, ACK, HW commit, PullHint wake,
// and idle runtime eviction flow without migration, retention, snapshot install,
// or leader repair semantics.
package channel
