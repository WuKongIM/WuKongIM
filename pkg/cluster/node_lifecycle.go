package cluster

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/internal/lifecycle"
	"github.com/WuKongIM/WuKongIM/pkg/dataformat"
)

// Start starts the node runtime and hosted background loops.
func (n *Node) Start(ctx context.Context) (err error) {
	if n == nil {
		return ErrNotStarted
	}
	// A failed startup or interrupted Stop may still own accepted RPCs. Join
	// that generation before creating replacement transport or storage owners.
	if n.group.HasStarted() && (!n.started.Load() || n.stopping.Load()) {
		if err := n.Stop(ctx); err != nil {
			return err
		}
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := dataformat.Check(n.cfg.DataDir); err != nil {
		return err
	}
	started := false
	createdDefaultChannels := false
	defer func() {
		if lifecycle.IsStopIncomplete(err) {
			n.stopping.Store(true)
			return
		}
		if !started && createdDefaultChannels {
			n.discardDefaultChannels()
			n.markChannelsReady(false)
		}
		if !started {
			n.discardDefaultSlots()
			if n.defaultProposer {
				n.proposer = nil
				n.defaultProposer = false
			}
			n.discardDefaultControl()
			n.discardDefaultTransport()
		}
	}()
	createdDefaultChannels, err = n.ensureDefaultRuntime()
	if err != nil {
		return err
	}
	n.invalidateWriteProbeProof()
	n.stopping.Store(false)
	resources := n.startResources()
	if err := n.group.Start(ctx, resources...); err != nil {
		n.started.Store(false)
		return err
	}
	if n.control != nil {
		if err := n.control.Start(ctx); err != nil {
			return errors.Join(err, n.rollbackStartupResources(ctx, false))
		}
		snapshot, err := n.initialControlSnapshot(ctx)
		if err != nil {
			return errors.Join(err, n.rollbackStartupResources(ctx, true))
		}
		if err := n.applySnapshot(ctx, snapshot); err != nil {
			return errors.Join(err, n.rollbackStartupResources(ctx, true))
		}
	}
	n.started.Store(true)
	n.startTaskReconcileLoop()
	n.startWatchLoop()
	n.startPreferredLeaderReconcileLoop()
	n.startSlotLeaderLoop()
	n.markChannelsReady(n.channels != nil)
	n.startChannelTickLoop()
	n.startHealthReportLoop()
	n.startChannelRetentionGCLoop()
	n.startChannelMigrationLoop()
	started = true
	return nil
}

// rollbackStartupResources joins transport users while Controller and runtime
// dependencies remain available. The normal retryable Stop owns later cleanup.
func (n *Node) rollbackStartupResources(ctx context.Context, controlStarted bool) error {
	err := n.group.Quiesce(ctx)
	if lifecycle.IsStopIncomplete(err) {
		return err
	}
	if controlStarted && n.control != nil {
		if stopErr := ctxErr(ctx); stopErr != nil {
			return errors.Join(err, &nodeStopIncompleteError{cause: stopErr})
		}
		stopErr := n.control.Stop(ctx)
		if stopErr != nil && ctx.Err() != nil && errors.Is(stopErr, ctx.Err()) {
			return errors.Join(err, &nodeStopIncompleteError{cause: stopErr})
		}
		err = errors.Join(err, stopErr)
		if lifecycle.IsStopIncomplete(err) {
			return err
		}
	}
	return errors.Join(err, n.group.Stop(ctx))
}

func (n *Node) startResources() []lifecycle.NamedResource {
	if n == nil || !n.defaultTransport || n.transportServer == nil || n.transportClient == nil {
		return n.resources
	}
	resources := make([]lifecycle.NamedResource, 0, len(n.resources)+2)
	resources = append(resources,
		lifecycle.NamedResource{Name: "cluster-transport-server", Resource: transportServerResource{server: n.transportServer, addr: n.cfg.ListenAddr}},
		lifecycle.NamedResource{Name: "cluster-transport-client", Resource: transportClientResource{client: n.transportClient}},
	)
	resources = append(resources, n.resources...)
	return resources
}

// Stop stops the node runtime and rejects new foreground work.
func (n *Node) Stop(ctx context.Context) error {
	if n == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	n.mu.Lock()
	n.stopping.Store(true)
	n.snapshot.RoutesReady = false
	n.snapshot.SlotsReady = false
	n.snapshot.ChannelsReady = false
	n.mu.Unlock()
	n.invalidateWriteProbeProof()
	// Stop entry is the mutation fence for background preferred-leader work.
	// An already-issued nonblocking transfer may finish, but no stale intent may
	// cross the generation guard while the other background loops wind down.
	n.invalidatePreferredLeaderIntent()
	n.stopHealthReportLoop(ctx)
	n.stopWatchLoop()
	n.stopTaskReconcileLoop()
	n.stopPreferredLeaderReconcileLoop()
	n.closeRouteAuthorityWatchers()
	n.stopSlotLeaderLoop()
	n.stopChannelTickLoop()
	n.stopChannelRetentionGCLoop()
	n.stopChannelMigrationLoop()
	var errs []error
	// Incoming handlers may still call Slots, Controller, Channels or nested
	// outbound RPCs. Join server then client ownership before closing any of
	// those dependencies, even when the stopping caller is already canceled.
	if err := n.group.Quiesce(ctx); err != nil {
		errs = append(errs, err)
		if lifecycle.IsStopIncomplete(err) {
			return errors.Join(errs...)
		}
	}
	// Controller.Stop rejects an expired context before releasing its backend.
	// A completed transport join does not authorize disposing that live runtime.
	if err := ctxErr(ctx); err != nil && (n.started.Load() || n.group.HasStarted()) {
		return errors.Join(errors.Join(errs...), &nodeStopIncompleteError{cause: err})
	}
	if n.defaultSlotProxy != nil {
		n.defaultSlotProxy.CloseSendPermissionReads()
	}
	if n.defaultChannels {
		if n.channelRPCGateway != nil {
			n.channelRPCGateway.Clear()
		}
		if n.channelQuorumGateway != nil {
			n.channelQuorumGateway.Clear()
		}
	}
	if n.channels != nil {
		if err := n.channels.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if n.defaultChannels {
		n.channels = nil
		n.defaultChannels = false
		if n.defaultChannelReplication != nil {
			if err := n.defaultChannelReplication.Close(ctx); err != nil {
				errs = append(errs, err)
			}
			n.defaultChannelReplication = nil
		}
		if err := n.closeDefaultChannelStore(); err != nil {
			errs = append(errs, err)
		}
	}
	n.markChannelsReady(false)
	if n.control != nil {
		if err := n.control.Stop(ctx); err != nil {
			errs = append(errs, err)
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return errors.Join(errors.Join(errs...), &nodeStopIncompleteError{cause: err})
			}
		}
	}
	if err := n.group.Stop(ctx); err != nil {
		errs = append(errs, err)
		if lifecycle.IsStopIncomplete(err) {
			return errors.Join(errs...)
		}
	}
	if n.defaultSlots {
		if n.defaultSlotRuntime != nil {
			if err := n.defaultSlotRuntime.Close(); err != nil {
				errs = append(errs, err)
			}
			n.defaultSlotRuntime = nil
			n.slotRaftDiagnostics = nil
		}
		if n.defaultSlotRaftDB != nil {
			if err := n.defaultSlotRaftDB.Close(); err != nil {
				errs = append(errs, err)
			}
			n.defaultSlotRaftDB = nil
		}
		if n.defaultSlotMetaDB != nil {
			if err := n.defaultSlotMetaDB.Close(); err != nil {
				errs = append(errs, err)
			}
			n.defaultSlotMetaDB = nil
		}
		n.defaultSlotProxy = nil
		n.defaultSlotProposer = nil
		n.slotStatusRuntime = nil
		n.slots = nil
		if n.defaultTaskExecutor {
			n.tasks = nil
			n.defaultTaskExecutor = false
		}
		if n.defaultPreferredLeaderReconciler {
			n.preferredLeaderReconciler = nil
			n.defaultPreferredLeaderReconciler = false
		}
		n.defaultSlots = false
	}
	if n.defaultProposer {
		n.proposer = nil
		n.defaultProposer = false
	}
	n.discardDefaultControl()
	n.discardDefaultTransport()
	n.started.Store(false)
	return errors.Join(errs...)
}

// nodeStopIncompleteError retains runtime ownership when the caller budget
// expires after transport joins but before Controller cleanup can complete.
type nodeStopIncompleteError struct{ cause error }

func (e *nodeStopIncompleteError) Error() string {
	return "cluster: runtime cleanup incomplete: " + e.cause.Error()
}

func (e *nodeStopIncompleteError) Unwrap() error        { return e.cause }
func (e *nodeStopIncompleteError) StopIncomplete() bool { return true }

func (n *Node) closeDefaultChannelStore() error {
	if n == nil || n.defaultChannelStore == nil {
		return nil
	}
	err := n.defaultChannelStore.Close()
	n.defaultChannelStore = nil
	return err
}
