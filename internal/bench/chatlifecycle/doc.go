// Package chatlifecycle defines the deterministic model and narrow startup
// orchestration for a long-running chat lifecycle workload.
//
// It intentionally contains no target mutation, concrete transport, worker
// loop, or host credential logic. Runtime layers execute its validated plans
// through narrow public-API and WKProto interfaces and evaluate bounded
// observations against its thresholds.
package chatlifecycle
