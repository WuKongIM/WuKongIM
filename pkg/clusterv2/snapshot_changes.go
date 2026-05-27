package clusterv2

import (
	"reflect"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

type controlSnapshotChanges struct {
	nodes     bool
	slots     bool
	tasks     bool
	hashSlots bool
}

func snapshotChanges(previous, next control.Snapshot) controlSnapshotChanges {
	previous = previous.Clone()
	next = next.Clone()
	return controlSnapshotChanges{
		nodes:     !reflect.DeepEqual(previous.Nodes, next.Nodes),
		slots:     !reflect.DeepEqual(previous.Slots, next.Slots),
		tasks:     !reflect.DeepEqual(previous.Tasks, next.Tasks),
		hashSlots: !reflect.DeepEqual(previous.HashSlots, next.HashSlots),
	}
}
