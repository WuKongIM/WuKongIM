package cluster

import (
	"context"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type clusterconfigManager struct {
	clusterconfigServer *clusterconfig.Server // 分布式配置服务
	opts                *Options
	stopper             *syncutil.Stopper
	onMessage           func(m clusterconfig.Message)
	wklog.Log
}

func newClusterconfigManager(opts *Options) *clusterconfigManager {
	remoteCfgPath := clusterconfig.WithConfigPath(path.Join(opts.DataDir, "clusterconfig.json"))
	c := &clusterconfigManager{
		opts:    opts,
		Log:     wklog.NewWKLog("clusterconfigManager"),
		stopper: syncutil.NewStopper(),
	}
	c.clusterconfigServer = clusterconfig.New(opts.NodeID, clusterconfig.WithTransport(c), clusterconfig.WithReplicas(opts.Replicas()), remoteCfgPath)
	return c
}

func (c *clusterconfigManager) start() error {
	c.stopper.RunWorker(c.loop)
	return c.clusterconfigServer.Start()
}

func (c *clusterconfigManager) stop() {
	c.clusterconfigServer.Stop()
	c.stopper.Stop()
}

func (c *clusterconfigManager) loop() {
	tk := time.NewTicker(time.Millisecond * 200)
	for {
		select {
		case <-tk.C:
			if c.clusterconfigServer.IsLeader() {
				c.checkClusterConfig()
			}
		case <-c.stopper.ShouldStop():
			return
		}
	}
}

func (c *clusterconfigManager) checkClusterConfig() {
	clusterConfig := c.clusterconfigServer.ConfigManager().GetConfig()

	newCfg := clusterConfig.Clone()
	var hasChange = false
	// 初始化节点
	changed := c.initNodesIfNeed(newCfg)
	if changed {
		hasChange = true
	}

	// 初始化slots
	changed = c.initSlotsIfNeed(newCfg)
	if changed {
		hasChange = true
	}

	// 设置领导
	changed = c.electionLeaderIfNeed(newCfg)
	if changed {
		hasChange = true
	}

	if hasChange {
		newCfg.Version++
		err := c.clusterconfigServer.ProposeConfigChange(newCfg.Version, c.clusterconfigServer.ConfigManager().GetConfigDataByCfg(newCfg))
		if err != nil {
			c.Error("propose config change error", zap.Error(err))
		}
	}
}

// 初始化节点
func (c *clusterconfigManager) initNodesIfNeed(newCfg *pb.Config) bool {
	if len(newCfg.Nodes) == 0 && len(c.opts.InitNodes) > 0 {
		newNodes := make([]*pb.Node, 0, len(c.opts.InitNodes))
		for replicaID, clusterAddr := range c.opts.InitNodes {
			newNodes = append(newNodes, &pb.Node{
				Id:          replicaID,
				ClusterAddr: clusterAddr,
			})
		}
		c.clusterconfigServer.ConfigManager().AddOrUpdateNodes(newNodes, newCfg)
		return true
	}
	return false
}

// 初始化slots
func (c *clusterconfigManager) initSlotsIfNeed(newCfg *pb.Config) bool {
	if len(newCfg.Slots) == 0 {
		newSlots := make([]*pb.Slot, 0, c.opts.SlotCount)
		for i := uint32(0); i < c.opts.SlotCount; i++ {
			newSlots = append(newSlots, &pb.Slot{
				Id:           i,
				ReplicaCount: c.opts.SlotMaxReplicaCount,
			})
		}
		c.clusterconfigServer.ConfigManager().AddOrUpdateSlots(newSlots, newCfg)
		return true
	}
	return false
}

func (c *clusterconfigManager) electionLeaderIfNeed(newCfg *pb.Config) bool {
	if len(newCfg.Slots) == 0 || len(newCfg.Nodes) == 0 {
		return false
	}
	nodes := newCfg.Nodes
	replicas := make([]uint64, 0, len(nodes))
	for _, n := range nodes {
		replicas = append(replicas, n.Id)
	}
	replicaCount := c.opts.SlotMaxReplicaCount
	for _, slot := range newCfg.Slots {
		if len(slot.Replicas) > 0 {
			continue
		}
		if len(replicas) <= int(replicaCount) {
			slot.Replicas = replicas
		} else {
			slot.Replicas = make([]uint64, 0, replicaCount)
			for i := uint32(0); i < replicaCount; i++ {
				randomIndex := globalRand.Intn(len(replicas))
				slot.Replicas = append(slot.Replicas, replicas[randomIndex])
			}
		}
		randomIndex := globalRand.Intn(len(slot.Replicas))
		slot.Leader = slot.Replicas[randomIndex]
	}
	return true
}

// 获取初始节点
func (c *clusterconfigManager) getInitNodes(cfg *pb.Config) []*pb.Node {
	initNodes := make([]*pb.Node, 0, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		if !n.Join {
			initNodes = append(initNodes, n)
		}
	}
	return initNodes
}

func (c *clusterconfigManager) handleMessage(m clusterconfig.Message) {
	if c.onMessage != nil {
		c.onMessage(m)
	}
}

func (c *clusterconfigManager) Send(m clusterconfig.Message) error {
	return c.opts.Transport.Send(m.To, &proto.Message{
		MsgType: MsgClusterConfigMsg,
		Content: m.Marshal(),
	})
}

func (c *clusterconfigManager) OnMessage(f func(m clusterconfig.Message)) {
	c.onMessage = f
}

func (c *clusterconfigManager) GetConfigVersion() uint64 {
	return c.clusterconfigServer.ConfigManager().GetConfig().GetVersion()
}

func (c *clusterconfigManager) CloneConfig() *pb.Config {
	return c.clusterconfigServer.ConfigManager().GetConfig().Clone()
}

// 等待配置中的节点数量达到指定数量
func (c *clusterconfigManager) waitConfigNodeCount(count int, timeout time.Duration) error {
	tk := time.NewTicker(time.Millisecond * 20)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-tk.C:
			clusterConfig := c.clusterconfigServer.ConfigManager().GetConfig()
			if len(clusterConfig.Nodes) >= count {
				return nil
			}
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		}
	}
}

func (c *clusterconfigManager) waitConfigSlotCount(count uint32, timeout time.Duration) error {
	tk := time.NewTicker(time.Millisecond * 20)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		select {
		case <-tk.C:
			clusterConfig := c.clusterconfigServer.ConfigManager().GetConfig()
			if len(clusterConfig.Slots) >= int(count) {
				return nil
			}
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		}
	}
}
