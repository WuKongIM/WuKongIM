package cluster

import "testing"

func TestRestoreMaintenanceObserverQuiescesBeforeWriteFence(t *testing.T) {
	node := &Node{}
	observer := &maintenanceOrderObserver{node: node}
	node.cfg.MaintenanceObserver = observer

	node.setMaintenance(true)

	if observer.enabledSawMaintenance {
		t.Fatal("enabled observer saw maintenance fence before local quiescence")
	}
	if !node.maintenance.Load() {
		t.Fatal("maintenance fence is disabled after enabled observer returns")
	}

	node.setMaintenance(false)
	if observer.disabledSawMaintenance {
		t.Fatal("disabled observer saw maintenance fence after local resume")
	}
}

type maintenanceOrderObserver struct {
	node                   *Node
	enabledSawMaintenance  bool
	disabledSawMaintenance bool
}

func (o *maintenanceOrderObserver) RestoreMaintenanceChanged(enabled bool) {
	if enabled {
		o.enabledSawMaintenance = o.node.maintenance.Load()
		return
	}
	o.disabledSawMaintenance = o.node.maintenance.Load()
}
