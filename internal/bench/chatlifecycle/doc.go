// Package chatlifecycle defines the pure, deterministic configuration model for
// a long-running chat lifecycle workload.
//
// It intentionally contains no target mutation, transport, scheduler, or host
// credential logic. Later layers turn a validated Config into a deterministic
// plan, execute it through public APIs and WKProto, and evaluate bounded
// observations against its thresholds.
package chatlifecycle
