package chatlifecycle

// DriverCPUReport binds private joined evidence to an anonymous assignment.
// It never enters the worker protocol or native report/evidence structures.
type DriverCPUReport struct {
	RunHash        string                        `json:"run_hash"`
	AssignmentHash string                        `json:"assignment_hash"`
	WorkerID       uint64                        `json:"worker_id"`
	Diagnostics    *DriverCPUDiagnosticsSnapshot `json:"diagnostics"`
}

// WorkerDiagnosticOptions are process-local opt-ins, outside workload config.
// CPU reports use nonblocking delivery so exporter IO cannot delay Stop.
type WorkerDiagnosticOptions struct {
	Driver      bool
	AttemptLink bool
	// CPUClockProbe requires Driver and a CPUReports inbox; nil preserves the
	// existing diagnostic path without reading any additional OS clocks.
	CPUClockProbe func() DriverCPUClockSample
	// CPUReports stays open until every generation has stopped. Delivery is
	// attempted once after join and is discarded when this inbox cannot accept.
	CPUReports chan<- DriverCPUReport
}

// NewEngineWorkerGenerationFactoryWithDiagnostics configures the existing runtime.
func NewEngineWorkerGenerationFactoryWithDiagnostics(token string, options WorkerDiagnosticOptions) WorkerGenerationFactory {
	return engineWorkerGenerationFactory{
		benchToken:                   token,
		enableDriverDiagnostics:      options.Driver,
		enableAttemptLinkDiagnostics: options.AttemptLink,
		driverCPUClockProbe:          options.CPUClockProbe,
		driverCPUReports:             options.CPUReports,
	}
}

// publishDriverCPUReport observes only the Engine's detached post-join copy.
// A concurrent Engine.Stop waiter can return before the stopping owner clears
// its lifecycle state, so a nil copy must not consume this generation's once.
func (g *engineWorkerGeneration) publishDriverCPUReport() {
	if g.driverCPUReports == nil {
		return
	}
	snapshot := g.engine.DriverCPUDiagnosticsAfterStop()
	if snapshot == nil || snapshot.Generation != g.generation {
		return
	}
	g.driverCPUReportOnce.Do(func() {
		report := g.driverCPUReport
		report.Diagnostics = snapshot
		select {
		case g.driverCPUReports <- report:
		default:
		}
	})
}
