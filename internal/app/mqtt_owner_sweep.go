package app

import runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"

// wireMQTTOwnerSweeper joins local cleanup and fixed aggregate observations.
// Runtime owns deadlines and cancellation; no Session policy belongs here.
func (a *App) wireMQTTOwnerSweeper(owners *runtime.Owners) (*runtime.OwnerSweeper, error) {
	opts := runtime.OwnerSweeperOptions{Owners: owners, Registry: a.goroutines}
	if a.metrics != nil {
		opts.Observe = func(o runtime.OwnerSweepObservation) {
			m := a.metrics.MQTT
			m.ObserveOwnerSweep(o.Visited, o.Failures != 0)
			for _, s := range []struct {
				name  string
				value int
			}{
				{"held", o.Owners.Held}, {"pending", o.Owners.Pending}, {"active", o.Owners.Active},
				{"closing", o.Owners.Closing}, {"operations", o.Owners.Operations},
				{"deadlines", o.Owners.Deadlines}, {"uncertain", o.Owners.Uncertain},
			} {
				m.SetOwnerWork(s.name, s.value)
			}
		}
	}
	return runtime.NewOwnerSweeper(opts)
}
