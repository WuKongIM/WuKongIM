package clusterv2

import (
	"context"
	"time"
)

const (
	defaultTaskReconcileIdleInterval = time.Second
	maxTaskReconcileIdleInterval     = 5 * time.Second
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

func (n *Node) startTaskReconcileLoop() {
	if n == nil || n.control == nil || n.tasks == nil || n.taskReconcileCancel != nil {
		return
	}
	fastInterval := n.taskReconcileFastInterval()
	idleInterval := taskReconcileIdleInterval(fastInterval)
	ctx, cancel := context.WithCancel(context.Background())
	n.taskReconcileCancel = cancel
	n.taskReconcileWG.Add(1)
	go func() {
		defer n.taskReconcileWG.Done()
		timer := time.NewTimer(n.nextTaskReconcileInterval(fastInterval, idleInterval))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				nextInterval := idleInterval
				snapshot, err := n.control.LocalSnapshot(ctx)
				if err != nil {
					n.recordTaskReconcileError("snapshot", err)
					if n.hasCachedControlTasks() {
						nextInterval = fastInterval
					}
					timer.Reset(nextInterval)
					continue
				}
				if len(snapshot.Tasks) == 0 {
					n.clearTaskReconcileError()
					timer.Reset(nextInterval)
					continue
				}
				nextInterval = fastInterval
				if err := n.reconcileTasks(ctx, snapshot); err != nil {
					n.recordTaskReconcileError("reconcile", err)
				} else {
					n.clearTaskReconcileError()
				}
				timer.Reset(nextInterval)
			}
		}
	}()
}

func (n *Node) taskReconcileFastInterval() time.Duration {
	interval := n.cfg.Slots.TickInterval * 5
	if interval <= 0 {
		return 100 * time.Millisecond
	}
	return interval
}

func taskReconcileIdleInterval(fast time.Duration) time.Duration {
	interval := fast * 20
	if interval < defaultTaskReconcileIdleInterval {
		return defaultTaskReconcileIdleInterval
	}
	if interval > maxTaskReconcileIdleInterval {
		return maxTaskReconcileIdleInterval
	}
	return interval
}

func (n *Node) nextTaskReconcileInterval(fast, idle time.Duration) time.Duration {
	if n.hasCachedControlTasks() {
		return fast
	}
	return idle
}

func (n *Node) hasCachedControlTasks() bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.controlSnapshot.Tasks) > 0
}

func (n *Node) stopTaskReconcileLoop() {
	if n == nil || n.taskReconcileCancel == nil {
		return
	}
	n.taskReconcileCancel()
	n.taskReconcileWG.Wait()
	n.taskReconcileCancel = nil
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
