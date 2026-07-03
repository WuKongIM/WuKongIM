// Package planner contains pure Controller planning helpers.
//
// Planners compare an input cluster-state snapshot with desired Controller
// invariants and return command proposals without performing IO. Keeping this
// package pure makes bootstrap and reconciliation decisions easy to test before
// callers submit commands through Raft.
package planner
