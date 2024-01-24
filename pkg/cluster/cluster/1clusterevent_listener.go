package cluster

import (
	"bytes"
	"io"
	"os"
	"path"
	"time"

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
	msgs                 []EventMessage
	wklog.Log
}

func newClusterEventListener(opts *Options) *clusterEventListener {
	localCfgPath := path.Join(opts.DataDir, "localconfig.json")
	c := &clusterEventListener{
		clusterconfigManager: newClusterconfigManager(opts),
		localCfgPath:         localCfgPath,
		Log:                  wklog.NewWKLog("ClusterEventListener"),
		stopper:              syncutil.NewStopper(),
		clusterEventC:        make(chan ClusterEvent),
		localConfg:           &pb.Config{},
	}

	err := c.loadLocalConfig()
	if err != nil {
		c.Panic("load local config error", zap.Error(err))
	}

	return c
}

func (c *clusterEventListener) Start() error {
	c.stopper.RunWorker(c.loopEvent)
	return c.clusterconfigManager.start()
}

func (c *clusterEventListener) Stop() {
	c.localFile.Close()
	c.clusterconfigManager.stop()
	c.stopper.Stop()
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

func (c *clusterEventListener) Step(m EventMessage) {
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
	if c.localConfg.Version == c.clusterconfigManager.GetConfigVersion() {
		return
	}
	if c.snapshotConfig == nil {
		c.snapshotConfig = c.clusterconfigManager.CloneConfig()
		return
	}

	c.checkClusterEventForNode() // 检查节点变化

	c.checkClusterEventForSlot() // 检查槽变化

	if len(c.msgs) > 0 {
		c.triggerClusterEvent(ClusterEvent{Messages: c.msgs})
	} else { // 没有事件了 说明配置与快照达到一致
		c.localConfg = c.snapshotConfig
		err := c.saveLocalConfig()
		if err != nil {
			c.Error("save local config error", zap.Error(err))
		}
		c.snapshotConfig = nil
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

func (c *clusterEventListener) checkNodeChange(localNode *pb.Node, snapshotNode *pb.Node) {
	if localNode.Id != snapshotNode.Id {
		return
	}
	if !bytes.Equal(localNode.Extra, snapshotNode.Extra) {
		c.msgs = append(c.msgs, EventMessage{
			Type:  ClusterEventTypeNodeUpdateExtra,
			Nodes: []*pb.Node{snapshotNode},
		})
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
	f, err := os.OpenFile(c.localCfgPath, os.O_CREATE|os.O_RDWR, os.ModePerm)
	if err != nil {
		return err
	}
	c.localFile = f
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		var cfg *pb.Config
		if err := wkutil.ReadJSONByByte(data, cfg); err != nil {
			return err
		}
		c.localConfg = cfg
	}
	return nil
}

func (c *clusterEventListener) saveLocalConfig() error {
	data := c.getLocalConfigData()
	if _, err := c.localFile.WriteAt(data, 0); err != nil {
		return err
	}
	return nil
}

func (c *clusterEventListener) getLocalConfigData() []byte {
	return []byte(wkutil.ToJSON(c.localConfg))
}

type ClusterEventType int

const (
	ClusterEventTypeNone             ClusterEventType = iota
	ClusterEventTypeNodeInit                          // 节点初始化
	ClusterEventTypeNodeAdd                           // 节点添加
	ClusterEventTypeNodeRemove                        // 节点移除
	ClusterEventTypeNodeUpdate                        // 节点更新
	ClusterEventTypeNodeUpdateExtra                   // 节点更新扩展数据
	ClusterEventTypeNodeUpdateOnline                  // 节点更新在线状态
	ClusterEventTypeSlotInit                          // 槽初始化
	ClusterEventTypeSlotAdd                           // 槽添加
	ClusterEventTypeSlotMigrate                       // 槽迁移
)

type ClusterEvent struct {
	Messages []EventMessage
}

var EmptyClusterEvent = ClusterEvent{}

func IsEmptyClusterEvent(event ClusterEvent) bool {
	return len(event.Messages) == 0
}

type SlotMigrate struct {
	SlotId uint32
	From   uint64
	To     uint64
}

type EventMessage struct {
	Type        ClusterEventType
	Nodes       []*pb.Node
	Slots       []*pb.Slot
	SlotMigrate SlotMigrate
}
