// Package server composes Controller dependencies behind a thin facade.
//
// Server connects local state reads, planner ticks, Raft proposals, and mirror
// sync without owning the durable model or transport details. It is intentionally
// small so tests and production wrappers can assemble Controller behavior while
// keeping the root runtime facade as the public integration point.
package server
