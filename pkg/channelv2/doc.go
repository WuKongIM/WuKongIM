// Package channelv2 is an experimental multiple-reactor channel log runtime.
//
// V0 validates append, follower apply, ACK, HW commit, PullHint wake,
// and idle runtime eviction flow without migration, retention, snapshot install,
// or leader repair semantics.
package channelv2
