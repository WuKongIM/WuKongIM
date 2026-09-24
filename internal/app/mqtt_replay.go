package app

import (
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// newMQTTReplayCoordinator wires fresh Slot metadata and foreground Node ports.
// It owns no background worker and does not enable product MQTT admission.
func newMQTTReplayCoordinator(node *cluster.Node, ids interface{ Next() uint64 }) (*sessioncase.ReplayCoordinator, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewReplayCoordinator(sessioncase.ReplayCoordinatorOptions{
		Metadata: channels.NewSlotMetaSource(node), Channels: node, MessageIDs: ids,
	})
}

// newMQTTReplayRetention composes ordered anchor/minimum reads through current
// cluster authority. Reclamation still requires a replicated source decision.
func newMQTTReplayRetention(node *cluster.Node) (*sessioncase.ReplayRetention, error) {
	if node == nil {
		return nil, sessioncase.ErrInvalid
	}
	return sessioncase.NewReplayRetention(sessioncase.ReplayRetentionOptions{
		Metadata: channels.NewSlotMetaSource(node), Channels: node, Store: node,
	})
}

// newMQTTReplayRetirement binds ordered permission to foreground selection and
// native retirement commits. It does not start a worker or expose MQTT admission.
func newMQTTReplayRetirement(node *cluster.Node, ids interface{ Next() uint64 }) (*sessioncase.ReplayRetirement, error) {
	planner, err := newMQTTReplayRetention(node)
	if err != nil {
		return nil, err
	}
	return sessioncase.NewReplayRetirement(sessioncase.ReplayRetirementOptions{Retention: planner, Channels: node, MessageIDs: ids})
}

// newMQTTReplayWorker binds the managed loop to real discovery and turn ports.
// The caller owns start/stop/restore ordering relative to Node and its allocator.
func newMQTTReplayWorker(node *cluster.Node, ids interface{ Next() uint64 }, options runtime.ReplayWorkerOptions) (*runtime.ReplayWorker, error) {
	coordinator, err := newMQTTReplayCoordinator(node, ids)
	if err != nil {
		return nil, err
	}
	options.Source, options.Stepper = node, coordinator
	return runtime.NewReplayWorker(options)
}
