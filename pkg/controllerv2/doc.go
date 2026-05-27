// Package controllerv2 exposes the public ControllerV2 runtime facade.
//
// Callers should depend on this root package for ControllerV2 runtime startup,
// strongly typed cluster-state snapshots, state change events, Raft message
// stepping, proposal probes, and full-file state sync contracts. Subpackages
// contain the reusable Controller engine internals and should stay behind the
// facade for production integrations.
package controllerv2
