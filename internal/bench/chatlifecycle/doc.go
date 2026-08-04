// Package chatlifecycle defines the deterministic model, narrow startup
// orchestration, bounded message verifier, and redacted evidence recorder for
// a long-running chat lifecycle workload.
//
// It intentionally contains no target mutation, concrete transport, worker
// loop, or host credential logic. Runtime layers execute its validated plans
// through narrow public-API and WKProto interfaces and evaluate bounded
// observations against its thresholds.
package chatlifecycle
