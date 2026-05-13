package management

import (
	"context"
	"fmt"
	"sort"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	scaleInChannelScanPageLimit = 128
	scaleInChannelTaskScanLimit = 1024
)

type nodeScaleInChannelInventory struct {
	leaders          int
	replicas         int
	activeMigrations int
	scanned          bool
	partial          bool
	errText          string
}

func (a *App) loadNodeScaleInChannelInventory(ctx context.Context, nodeID uint64) nodeScaleInChannelInventory {
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil || a.channelMigration == nil {
		return nodeScaleInChannelInventory{
			partial: true,
			errText: "channel inventory dependencies are not configured",
		}
	}

	inventory := nodeScaleInChannelInventory{scanned: true}
	seenTasks := make(map[string]struct{})
	slotIDs := append([]multiraft.SlotID(nil), a.cluster.SlotIDs()...)
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })

	for _, slotID := range slotIDs {
		after := metadb.ChannelRuntimeMetaCursor{}
		for {
			page, cursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, scaleInChannelScanPageLimit)
			if err != nil {
				inventory.partial = true
				inventory.errText = scaleInChannelInventoryError(err)
				return inventory
			}
			for _, meta := range page {
				inventory.addRuntimeMeta(ctx, a.channelMigration, nodeID, meta, seenTasks)
				if inventory.partial {
					return inventory
				}
			}
			if done {
				break
			}
			if cursor == after {
				inventory.partial = true
				inventory.errText = "channel inventory scan made no cursor progress"
				return inventory
			}
			after = cursor
		}
	}

	tasks, hasMore, err := a.channelMigration.ListActiveChannelMigrationTasksForNode(ctx, nodeID, scaleInChannelTaskScanLimit)
	if err != nil {
		inventory.partial = true
		inventory.errText = scaleInChannelInventoryError(err)
		return inventory
	}
	for _, task := range tasks {
		if task.SourceNode == nodeID || task.TargetNode == nodeID {
			inventory.addMigrationTask(task, seenTasks)
		}
	}
	if hasMore {
		inventory.partial = true
		inventory.errText = fmt.Sprintf("active channel migration scan exceeded limit %d", scaleInChannelTaskScanLimit)
	}
	return inventory
}

func (i *nodeScaleInChannelInventory) addRuntimeMeta(ctx context.Context, store ChannelMigrationStore, nodeID uint64, meta metadb.ChannelRuntimeMeta, seenTasks map[string]struct{}) {
	referencesTarget := false
	if meta.Leader == nodeID {
		i.leaders++
		referencesTarget = true
	}
	if scaleInUint64sContain(meta.Replicas, nodeID) {
		i.replicas++
		referencesTarget = true
	}
	if scaleInUint64sContain(meta.ISR, nodeID) {
		referencesTarget = true
	}
	if !referencesTarget {
		return
	}

	task, ok, err := store.GetActiveChannelMigrationTask(ctx, meta.ChannelID, meta.ChannelType)
	if err != nil {
		i.partial = true
		i.errText = scaleInChannelInventoryError(err)
		return
	}
	if ok {
		i.addMigrationTask(task, seenTasks)
	}
}

func (i *nodeScaleInChannelInventory) addMigrationTask(task metadb.ChannelMigrationTask, seenTasks map[string]struct{}) {
	key := scaleInChannelMigrationTaskKey(task)
	if _, ok := seenTasks[key]; ok {
		return
	}
	seenTasks[key] = struct{}{}
	i.activeMigrations++
}

func scaleInUint64sContain(values []uint64, needle uint64) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func scaleInChannelMigrationTaskKey(task metadb.ChannelMigrationTask) string {
	if task.TaskID != "" {
		return task.TaskID
	}
	return fmt.Sprintf("%s:%d:%d:%d:%d", task.ChannelID, task.ChannelType, task.Kind, task.SourceNode, task.TargetNode)
}

func scaleInChannelInventoryError(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) <= 512 {
		return text
	}
	return text[:512]
}
