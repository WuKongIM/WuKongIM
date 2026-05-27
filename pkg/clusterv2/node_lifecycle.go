package clusterv2

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/internal/lifecycle"
)

// Start starts the node runtime and hosted background loops.
func (n *Node) Start(ctx context.Context) error {
	if n == nil {
		return ErrNotStarted
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	createdDefaultChannels, err := n.ensureDefaultRuntime()
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started && createdDefaultChannels {
			n.discardDefaultChannels()
			n.markChannelsReady(false)
		}
		if !started {
			n.discardDefaultSlots()
			n.discardDefaultControl()
			n.discardDefaultTransport()
		}
	}()
	n.stopping.Store(false)
	resources := n.startResources()
	if err := n.group.Start(ctx, resources...); err != nil {
		n.started.Store(false)
		return err
	}
	if n.control != nil {
		if err := n.control.Start(ctx); err != nil {
			_ = n.group.Stop(ctx)
			return err
		}
		snapshot, err := n.initialControlSnapshot(ctx)
		if err != nil {
			_ = n.control.Stop(ctx)
			_ = n.group.Stop(ctx)
			return err
		}
		if err := n.applySnapshot(ctx, snapshot); err != nil {
			_ = n.control.Stop(ctx)
			_ = n.group.Stop(ctx)
			return err
		}
		n.startWatchLoop()
	}
	n.startSlotLeaderLoop()
	n.markChannelsReady(n.channels != nil)
	n.startChannelTickLoop()
	n.started.Store(true)
	started = true
	return nil
}

func (n *Node) startResources() []lifecycle.NamedResource {
	if n == nil || !n.defaultTransport || n.transportServer == nil || n.transportClient == nil {
		return n.resources
	}
	resources := make([]lifecycle.NamedResource, 0, len(n.resources)+2)
	resources = append(resources,
		lifecycle.NamedResource{Name: "clusterv2-transport-server", Resource: transportServerResource{server: n.transportServer, addr: n.cfg.ListenAddr}},
		lifecycle.NamedResource{Name: "clusterv2-transport-client", Resource: transportClientResource{client: n.transportClient}},
	)
	resources = append(resources, n.resources...)
	return resources
}

// Stop stops the node runtime and rejects new foreground work.
func (n *Node) Stop(ctx context.Context) error {
	if n == nil {
		return nil
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	n.stopping.Store(true)
	if n.watchCancel != nil {
		n.watchCancel()
	}
	n.stopSlotLeaderLoop()
	n.stopChannelTickLoop()
	var errs []error
	if n.channels != nil {
		if err := n.channels.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if n.defaultChannels {
		n.channels = nil
		n.defaultChannels = false
		if err := n.closeDefaultChannelStore(); err != nil {
			errs = append(errs, err)
		}
	}
	n.markChannelsReady(false)
	if n.control != nil {
		if err := n.control.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := n.group.Stop(ctx); err != nil {
		errs = append(errs, err)
	}
	if n.defaultSlots {
		if n.defaultSlotRuntime != nil {
			if err := n.defaultSlotRuntime.Close(); err != nil {
				errs = append(errs, err)
			}
			n.defaultSlotRuntime = nil
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
		n.defaultSlotProposer = nil
		n.slots = nil
		n.defaultSlots = false
	}
	n.discardDefaultControl()
	n.discardDefaultTransport()
	n.started.Store(false)
	return errors.Join(errs...)
}

func (n *Node) closeDefaultChannelStore() error {
	if n == nil || n.defaultChannelStore == nil {
		return nil
	}
	err := n.defaultChannelStore.Close()
	n.defaultChannelStore = nil
	return err
}
