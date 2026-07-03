// Package planner contains pure ControllerV2 planning helpers.
//
// Planners compare an input cluster-state snapshot with desired ControllerV2
// invariants and return command proposals without performing IO. Keeping this
// package pure makes bootstrap and reconciliation decisions easy to test before
// callers submit commands through Raft.
package planner
