package clusterv2

import (
	"context"
	"time"
)

func (n *Node) startWatchLoop() {
	if n == nil || n.control == nil || n.watchCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.watchCancel = cancel
	watch := n.control.Watch()
	n.watchWG.Add(1)
	go func() {
		defer n.watchWG.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-watch:
				if !ok {
					return
				}
				_ = n.applySnapshot(ctx, ev.Snapshot)
			}
		}
	}()
}

func (n *Node) stopWatchLoop() {
	if n == nil || n.watchCancel == nil {
		return
	}
	n.watchCancel()
	n.watchWG.Wait()
	n.watchCancel = nil
}

func (n *Node) startChannelTickLoop() {
	if n == nil || n.channels == nil || n.channelTickCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.channelTickCancel = cancel
	n.channelTickWG.Add(1)
	go func() {
		defer n.channelTickWG.Done()
		ticker := time.NewTicker(n.cfg.Channel.TickInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = n.channels.Tick(ctx)
			}
		}
	}()
}

func (n *Node) stopChannelTickLoop() {
	if n == nil || n.channelTickCancel == nil {
		return
	}
	n.channelTickCancel()
	n.channelTickWG.Wait()
	n.channelTickCancel = nil
}

func (n *Node) startChannelRetentionGCLoop() {
	if n == nil || n.channelRetentionCancel != nil || !n.cfg.ChannelRetention.PhysicalGCEnabled {
		return
	}
	if n.channels == nil || n.defaultChannelStore == nil || n.defaultSlotMetaDB == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	n.channelRetentionCancel = cancel
	n.channelRetentionWG.Add(1)
	go func() {
		defer n.channelRetentionWG.Done()
		ticker := time.NewTicker(n.cfg.ChannelRetention.ScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = n.RunChannelRetentionGCOnce(ctx)
			}
		}
	}()
}

func (n *Node) stopChannelRetentionGCLoop() {
	if n == nil || n.channelRetentionCancel == nil {
		return
	}
	n.channelRetentionCancel()
	n.channelRetentionWG.Wait()
	n.channelRetentionCancel = nil
}
