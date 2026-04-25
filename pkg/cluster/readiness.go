package cluster

import (
	"context"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	defaultForwardRetryInterval     = 50 * time.Millisecond
	defaultControllerRetryInterval  = 100 * time.Millisecond
	defaultManagedSlotPollInterval  = 100 * time.Millisecond
	defaultManagedSlotsReadyPoll    = 100 * time.Millisecond
	minimumDerivedRetryOrPollPeriod = 5 * time.Millisecond
)

func (c *Cluster) timeoutConfig() Timeouts {
	if c == nil {
		t := Timeouts{}
		t.applyDefaults()
		return t
	}
	t := c.cfg.Timeouts
	t.applyDefaults()
	if c.controllerLeaderWaitTimeout > 0 {
		t.ControllerLeaderWait = c.controllerLeaderWaitTimeout
	}
	return t
}

func (c *Cluster) withControllerTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, c.timeoutConfig().ControllerRequest)
}

func (c *Cluster) controllerObservationInterval() time.Duration {
	return c.timeoutConfig().ControllerObservation
}

func (c *Cluster) observationHeartbeatInterval() time.Duration {
	return c.timeoutConfig().ObservationHeartbeatInterval
}

func (c *Cluster) observationRuntimeScanInterval() time.Duration {
	return c.timeoutConfig().ObservationRuntimeScanInterval
}

func (c *Cluster) observationRuntimeFlushDebounce() time.Duration {
	return c.timeoutConfig().ObservationRuntimeFlushDebounce
}

func (c *Cluster) observationRuntimeFullSyncInterval() time.Duration {
	return c.timeoutConfig().ObservationRuntimeFullSyncInterval
}

func (c *Cluster) observationSlowSyncInterval() time.Duration {
	return c.timeoutConfig().ObservationSlowSyncInterval
}

func (c *Cluster) plannerSafetyInterval() time.Duration {
	return c.timeoutConfig().PlannerSafetyInterval
}

func (c *Cluster) plannerWakeDebounce() time.Duration {
	return c.timeoutConfig().PlannerWakeDebounce
}

func (c *Cluster) forwardRetryInterval() time.Duration {
	return scaleDerivedInterval(c.timeoutConfig().ForwardRetryBudget, defaultForwardRetryBudget, defaultForwardRetryInterval)
}

func (c *Cluster) controllerRetryInterval() time.Duration {
	return scaleDerivedInterval(c.controllerLeaderWaitDuration(), defaultControllerLeaderWaitTimeout, defaultControllerRetryInterval)
}

func (c *Cluster) managedSlotsReadyPollInterval() time.Duration {
	return scaleDerivedInterval(c.timeoutConfig().ControllerObservation, defaultControllerObservationTimeout, defaultManagedSlotsReadyPoll)
}

func (c *Cluster) managedSlotLeaderPollInterval() time.Duration {
	return scaleDerivedInterval(c.timeoutConfig().ManagedSlotLeaderWait, defaultManagedSlotLeaderWaitTimeout, defaultManagedSlotPollInterval)
}

func (c *Cluster) managedSlotCatchUpPollInterval() time.Duration {
	return scaleDerivedInterval(c.timeoutConfig().ManagedSlotCatchUp, defaultManagedSlotCatchUpTimeout, defaultManagedSlotPollInterval)
}

func (c *Cluster) managedSlotLeaderMovePollInterval() time.Duration {
	return scaleDerivedInterval(c.timeoutConfig().ManagedSlotLeaderMove, defaultManagedSlotLeaderMoveTimeout, defaultManagedSlotPollInterval)
}

func (c *Cluster) configChangeRetryInterval() time.Duration {
	return scaleDerivedInterval(c.timeoutConfig().ConfigChangeRetryBudget, defaultConfigChangeRetryBudget, defaultForwardRetryInterval)
}

func (c *Cluster) leaderTransferRetryInterval() time.Duration {
	return scaleDerivedInterval(c.timeoutConfig().LeaderTransferRetryBudget, defaultLeaderTransferRetryBudget, defaultForwardRetryInterval)
}

func scaleDerivedInterval(actual, defaultActual, defaultInterval time.Duration) time.Duration {
	if defaultInterval <= 0 {
		return 0
	}
	if actual <= 0 {
		actual = defaultActual
	}
	if actual <= 0 || defaultActual <= 0 {
		return defaultInterval
	}
	interval := time.Duration(int64(actual) * int64(defaultInterval) / int64(defaultActual))
	if interval < minimumDerivedRetryOrPollPeriod {
		return minimumDerivedRetryOrPollPeriod
	}
	return interval
}

func buildRuntimeView(now time.Time, slotID multiraft.SlotID, status multiraft.Status, peers []multiraft.NodeID, observedConfigEpoch uint64) controllermeta.SlotRuntimeView {
	currentPeers := make([]uint64, 0, len(peers))
	for _, peer := range peers {
		currentPeers = append(currentPeers, uint64(peer))
	}

	view := controllermeta.SlotRuntimeView{
		SlotID:       uint32(slotID),
		CurrentPeers: currentPeers,
		LeaderID:     uint64(status.LeaderID),
		ObservedConfigEpoch: observedConfigEpoch,
		LastReportAt: now,
	}
	if len(currentPeers) > 0 {
		view.HealthyVoters = uint32(len(currentPeers))
		view.HasQuorum = status.LeaderID != 0
	}
	return view
}
