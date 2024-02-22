package cluster

import (
	"context"
	"io"
	"os"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type clusterEventListener struct {
	clusterconfigManager *clusterconfigManager // 分布式配置服务
	localConfg           *pb.Config            // 本地配置
	localFile            *os.File              // 本地配置文件
	localCfgPath         string                // 本地配置文件路径
	snapshotConfig       *pb.Config            // 快照配置
	stopper              *syncutil.Stopper
	clusterEventC        chan ClusterEvent
	opts                 *Options
	msgs                 []EventMessage
	wklog.Log
}

func newClusterEventListener(opts *Options) *clusterEventListener {
	localCfgPath := path.Join(opts.DataDir, "localconfig.json")
	c := &clusterEventListener{
		localCfgPath:  localCfgPath,
		Log:           wklog.NewWKLog("ClusterEventListener"),
		stopper:       syncutil.NewStopper(),
		clusterEventC: make(chan ClusterEvent),
		localConfg:    &pb.Config{},
		opts:          opts,
	}

	c.clusterconfigManager = newClusterconfigManager(opts)

	err := c.loadLocalConfig()
	if err != nil {
		c.Panic("load local config error", zap.Error(err))
	}

	return c
}

func (c *clusterEventListener) start() error {
	c.stopper.RunWorker(c.loopEvent)
	return c.clusterconfigManager.start()
}

func (c *clusterEventListener) stop() {
	c.stopper.Stop()
	c.localFile.Close()
	c.clusterconfigManager.stop()

}

func (c *clusterEventListener) wait() ClusterEvent {
	select {
	case event := <-c.clusterEventC:
		return event
	case <-c.stopper.ShouldStop():
		return EmptyClusterEvent
	}
}

func (c *clusterEventListener) loopEvent() {
	tk := time.NewTicker(time.Millisecond * 100)
	for {
		select {
		case <-tk.C:
			c.checkLocalClusterEvent() // 检查本地分布式事件

		case <-c.stopper.ShouldStop():
			return

		}
	}
}

// 本地所有节点
func (c *clusterEventListener) localAllNodes() []*pb.Node {
	return c.localConfg.Nodes
}

func (c *clusterEventListener) localAllSlot() []*pb.Slot {
	return c.localConfg.Slots
}

func (c *clusterEventListener) step(m EventMessage) {
	if m.Type == ClusterEventTypeNodeUpdateApiServerAddr {
		return
	}
	for _, n := range m.Nodes {
		exist := false
		for i, localNode := range c.localConfg.Nodes {
			if n.Id == localNode.Id {
				exist = true
				c.localConfg.Nodes[i] = n
				break
			}
		}
		if !exist {
			c.localConfg.Nodes = append(c.snapshotConfig.Nodes, n)
		}
	}

	for _, s := range m.Slots {
		exist := false
		for i, localSlot := range c.localConfg.Slots {
			if s.Id == localSlot.Id {
				exist = true
				c.localConfg.Slots[i] = s
				break
			}
		}
		if !exist {
			c.localConfg.Slots = append(c.localConfg.Slots, s)
		}
	}
}

func (c *clusterEventListener) advance() {
	c.acceptedEvent()
}

func (c *clusterEventListener) checkLocalClusterEvent() {
	if len(c.msgs) > 0 { // 还有事件没处理掉，不再检查
		return
	}
	if c.localConfg.Version == c.clusterconfigManager.getConfigVersion() {
		return
	}
	if c.snapshotConfig == nil {
		c.snapshotConfig = c.clusterconfigManager.cloneConfig()
		return
	}

	c.checkClusterEventForNode() // 检查节点变化

	c.checkClusterEventForSlot() // 检查槽变化

	c.checkClusterEventSlotMigrate() // 检查槽迁移

	if len(c.msgs) > 0 {
		c.printMsgs()
		c.triggerClusterEvent(ClusterEvent{Messages: c.msgs})
	} else { // 没有事件了 说明配置与快照达到一致
		c.Debug("save local config")
		c.localConfg = c.snapshotConfig
		err := c.saveLocalConfig()
		if err != nil {
			c.Error("save local config error", zap.Error(err))
		}
		c.snapshotConfig = nil
	}
}

func (c *clusterEventListener) printMsgs() {
	for _, m := range c.msgs {
		c.Debug("event message", zap.Any("type", m.Type), zap.Int("nodes", len(m.Nodes)), zap.Int("slots", len(m.Slots)), zap.Int("slotMigrates", len(m.SlotMigrates)))
	}
}

func (c *clusterEventListener) checkClusterEventForNode() {
	snapshotConfig := c.snapshotConfig
	if len(c.localConfg.Nodes) == 0 && len(snapshotConfig.Nodes) > 0 {
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeNodeAdd,
			Nodes: snapshotConfig.Nodes,
		})
	} else if len(c.localConfg.Nodes) > 0 && len(snapshotConfig.Nodes) == 0 {
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeNodeRemove,
			Nodes: c.localConfg.Nodes,
		})
	} else if len(c.localConfg.Nodes) > 0 && len(snapshotConfig.Nodes) > 0 {
		for _, node := range snapshotConfig.Nodes {
			exist := false
			for _, localNode := range c.localConfg.Nodes {
				if node.Id == localNode.Id {
					exist = true
					c.checkNodeChange(localNode, node)
					break
				}
			}
			if !exist {
				c.msgs = append(c.msgs, EventMessage{
					Type:  ClusterEventTypeNodeAdd,
					Nodes: []*pb.Node{node},
				})
			}
		}
		for _, localNode := range c.localConfg.Nodes {
			exist := false
			for _, node := range snapshotConfig.Nodes {
				if node.Id == localNode.Id {
					exist = true
					break
				}
			}
			if !exist {
				c.msgs = append(c.msgs, EventMessage{
					Type:  ClusterEventTypeNodeRemove,
					Nodes: []*pb.Node{localNode},
				})
			}
		}
	}

	// 检查当前节点的apiServerAddr是否变化
	currentNode := c.clusterconfigManager.node(c.opts.NodeID)
	if currentNode != nil && currentNode.ApiServerAddr != c.opts.ApiServerAddr {
		currentNodeClone := currentNode.Clone()
		currentNodeClone.ApiServerAddr = c.opts.ApiServerAddr
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeNodeUpdateApiServerAddr,
			Nodes: []*pb.Node{currentNodeClone},
		})
	}
}

func (c *clusterEventListener) checkClusterEventForSlot() {
	snapshotConfig := c.snapshotConfig
	if len(c.localConfg.Slots) == 0 && len(snapshotConfig.Slots) > 0 {
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeSlotAdd,
			Slots: snapshotConfig.Slots,
		})
	} else if len(c.localConfg.Slots) > 0 && len(snapshotConfig.Slots) == 0 { // 现在不存在移除slot的逻辑，所以这里没代码
	} else if len(c.localConfg.Slots) > 0 && len(snapshotConfig.Slots) > 0 { // 本地有slot，快照也有slot， 检查是否有变化
		for _, slot := range snapshotConfig.Slots {
			exist := false
			for _, localSlot := range c.localConfg.Slots {
				if slot.Id == localSlot.Id {
					exist = true
					c.checkSlotChange(localSlot, slot)
					break
				}
			}
			if !exist {
				c.msgs = append(c.msgs, EventMessage{
					Type:  ClusterEventTypeSlotAdd,
					Slots: []*pb.Slot{slot},
				})
			}
		}
	}
}

func (c *clusterEventListener) checkClusterEventSlotMigrate() {
	leaderconfig := c.snapshotConfig

	exist := false
	for _, node := range leaderconfig.Nodes {
		if node.Id != c.opts.NodeID {
			continue
		}

		if len(node.Imports) == 0 {
			continue
		}
		var migrates []*SlotMigrate
		for _, imp := range node.Imports {
			if c.opts.existSlotMigrateFnc(imp.Slot) {
				continue
			}
			if imp.Status == pb.MigrateStatus_MigrateStatusWill {
				migrates = append(migrates, &SlotMigrate{
					Slot: imp.Slot,
					From: imp.From,
					To:   imp.To,
				})
			}
		}
		if len(migrates) > 0 {
			c.msgs = append(c.msgs, EventMessage{
				Type:         ClusterEventTypeSlotMigrate,
				SlotMigrates: migrates,
			})
			exist = true
		}
	}
	if exist {
		return
	}
	localCfg := c.localConfg
	for _, localNode := range localCfg.Nodes {
		if localNode.Id != c.opts.NodeID {
			continue
		}
		if len(localNode.Imports) == 0 {
			continue
		}
		var remoteNode *pb.Node
		for _, node := range leaderconfig.Nodes {
			if node.Id == localNode.Id {
				remoteNode = node
				break
			}
		}
		if remoteNode == nil {
			continue
		}
		for _, localImp := range localNode.Imports {
			existImp := false
			for _, remoteImp := range remoteNode.Imports {
				if localImp.Slot == remoteImp.Slot {
					existImp = true
					break
				}
			}
			if !existImp {
				c.opts.removeSlotMigrateFnc(localImp.Slot)
			}
		}
	}

}

func (c *clusterEventListener) checkNodeChange(localNode *pb.Node, snapshotNode *pb.Node) {
	if localNode.Id != snapshotNode.Id {
		return
	}

	if localNode.Online != snapshotNode.Online {
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeNodeUpdateOnline,
			Nodes: []*pb.Node{snapshotNode},
		})
	}
}

func (c *clusterEventListener) checkSlotChange(localSlot *pb.Slot, snapshotSlot *pb.Slot) {
	if localSlot.Id != snapshotSlot.Id {
		return
	}
	if snapshotSlot.Leader != 0 && localSlot.Leader != snapshotSlot.Leader {
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeSlotChange,
			Slots: []*pb.Slot{snapshotSlot},
		})
	} else if !wkutil.ArrayEqualUint64(localSlot.Replicas, snapshotSlot.Replicas) {
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeSlotChange,
			Slots: []*pb.Slot{snapshotSlot},
		})

	}

}

func (c *clusterEventListener) triggerClusterEvent(event ClusterEvent) {
	select {
	case c.clusterEventC <- event:
	case <-c.stopper.ShouldStop():
	}
}

func (c *clusterEventListener) acceptedEvent() {
	c.msgs = nil
}

func (c *clusterEventListener) loadLocalConfig() error {
	f, err := os.OpenFile(c.localCfgPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.ModePerm)
	if err != nil {
		return err
	}
	c.localFile = f
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		var cfg = &pb.Config{}
		if err := wkutil.ReadJSONByByte(data, cfg); err != nil {
			return err
		}
		c.localConfg = cfg
	}
	return nil
}

func (c *clusterEventListener) saveLocalConfig() error {
	data := c.getLocalConfigData()
	err := c.localFile.Truncate(0)
	if err != nil {
		return err
	}
	if _, err = c.localFile.WriteAt(data, 0); err != nil {
		return err
	}
	return nil
}

func (c *clusterEventListener) getLocalConfigData() []byte {
	return []byte(wkutil.ToJSON(c.localConfg))
}

func (c *clusterEventListener) handleMessage(msg clusterconfig.Message) {
	c.clusterconfigManager.handleMessage(msg)
}

// 等待配置中的节点数量达到指定数量
func (c *clusterEventListener) waitConfigNodeCount(count int, timeout time.Duration) error {
	tk := time.NewTicker(time.Millisecond * 20)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-tk.C:
			if len(c.localConfg.Nodes) >= count {
				return nil
			}
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		}
	}
}

func (c *clusterEventListener) waitConfigSlotCount(count uint32, timeout time.Duration) error {
	tk := time.NewTicker(time.Millisecond * 20)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-tk.C:
			if len(c.localConfg.Slots) >= int(count) {
				return nil
			}
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		}
	}
}

type ClusterEventType int

const (
	ClusterEventTypeNone                    ClusterEventType = iota
	ClusterEventTypeNodeInit                                 // 节点初始化
	ClusterEventTypeNodeAdd                                  // 节点添加
	ClusterEventTypeNodeRemove                               // 节点移除
	ClusterEventTypeNodeUpdate                               // 节点更新
	ClusterEventTypeNodeUpdateApiServerAddr                  // 节点更新api服务地址
	ClusterEventTypeNodeUpdateOnline                         // 节点更新在线状态
	ClusterEventTypeSlotInit                                 // 槽初始化
	ClusterEventTypeSlotAdd                                  // 槽添加
	ClusterEventTypeSlotChange                               // 槽领导者变更
	ClusterEventTypeSlotMigrate                              // 槽迁移
)

func (c ClusterEventType) String() string {
	switch c {
	case ClusterEventTypeNodeInit:
		return "NodeInit"
	case ClusterEventTypeNodeAdd:
		return "NodeAdd"
	case ClusterEventTypeNodeRemove:
		return "NodeRemove"
	case ClusterEventTypeNodeUpdate:
		return "NodeUpdate"
	case ClusterEventTypeNodeUpdateApiServerAddr:
		return "NodeUpdateApiServerAddr"
	case ClusterEventTypeNodeUpdateOnline:
		return "NodeUpdateOnline"
	case ClusterEventTypeSlotInit:
		return "SlotInit"
	case ClusterEventTypeSlotAdd:
		return "SlotAdd"
	case ClusterEventTypeSlotChange:
		return "SlotChange"
	case ClusterEventTypeSlotMigrate:
		return "SlotMigrate"
	}
	return "Unknown"

}

type ClusterEvent struct {
	Messages []EventMessage
}

var EmptyClusterEvent = ClusterEvent{}

func IsEmptyClusterEvent(event ClusterEvent) bool {
	return len(event.Messages) == 0
}

type EventMessage struct {
	Type         ClusterEventType
	Nodes        []*pb.Node
	Slots        []*pb.Slot
	SlotMigrates []*SlotMigrate
}

var EmptyEventMessage = EventMessage{}
