package clusterv2

import (
	"context"
	"time"
)

func (n *Node) startWatchLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	n.watchCancel = cancel
	watch := n.control.Watch()
	go func() {
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
