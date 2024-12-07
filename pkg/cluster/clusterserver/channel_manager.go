package cluster

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

var _ reactor.IRequest = &channelManager{}

type channelManager struct {
	channelReactor *reactor.Reactor
	opts           *Options
	s              *Server
	wklog.Log

	sync.RWMutex
}

func newChannelManager(s *Server) *channelManager {
	cm := &channelManager{
		opts: s.opts,
		s:    s,
		Log:  wklog.NewWKLog("channelManager"),
	}
	cm.channelReactor = reactor.New(reactor.NewOptions(
		reactor.WithNodeId(s.opts.NodeId),
		reactor.WithSend(cm.onSend),
		reactor.WithReactorType(reactor.ReactorTypeChannel),
		reactor.WithAutoSlowDownOn(true),
		reactor.WithRequest(cm),
		reactor.WithSubReactorNum(s.opts.ChannelReactorSubCount),
	))
	return cm
}

func (c *channelManager) start() error {
	return c.channelReactor.Start()
}

func (c *channelManager) stop() {
	c.channelReactor.Stop()
}

func (c *channelManager) add(ch *channel) {
	c.Lock()
	defer c.Unlock()
	c.channelReactor.AddHandler(ch.key, ch)
}

func (c *channelManager) remove(ch *channel) {
	c.Lock()
	defer c.Unlock()
	c.channelReactor.RemoveHandler(ch.key)
}

func (c *channelManager) get(channelId string, channelType uint8) reactor.IHandler {
	c.RLock()
	defer c.RUnlock()
	return c.channelReactor.Handler(wkutil.ChannelToKey(channelId, channelType))
}

func (c *channelManager) getOrCreateIfNotExist(channelId string, channelType uint8) reactor.IHandler {
	c.Lock()
	defer c.Unlock()

	handleKey := wkutil.ChannelToKey(channelId, channelType)

	handler := c.channelReactor.Handler(handleKey)
	if handler != nil {
		return handler
	}
	handler = newChannel(channelId, channelType, c.s)
	c.channelReactor.AddHandler(handleKey, handler)
	return handler

}

func (c *channelManager) getOrCreateIfNotExistWithHandleKey(handleKey string) reactor.IHandler {
	c.Lock()
	defer c.Unlock()

	channelId, channelType := wkutil.ChannelFromlKey(handleKey)

	handler := c.channelReactor.Handler(handleKey)
	if handler != nil {
		return handler
	}
	handler = newChannel(channelId, channelType, c.s)
	c.channelReactor.AddHandler(handleKey, handler)
	return handler

}

func (c *channelManager) exist(channelId string, channelType uint8) bool {
	c.RLock()
	defer c.RUnlock()
	return c.channelReactor.ExistHandler(wkutil.ChannelToKey(channelId, channelType))
}

// 频道数量
func (c *channelManager) channelCount() int {
	c.RLock()
	defer c.RUnlock()
	return c.channelReactor.HandlerLen()
}

func (c *channelManager) getWithHandleKey(handleKey string) reactor.IHandler {
	c.RLock()
	defer c.RUnlock()
	return c.channelReactor.Handler(handleKey)
}

func (c *channelManager) proposeAndWait(ctx context.Context, channelId string, channelType uint8, logs []replica.Log) ([]reactor.ProposeResult, error) {
	return c.channelReactor.ProposeAndWait(ctx, wkutil.ChannelToKey(channelId, channelType), logs)
}

func (c *channelManager) addMessage(m reactor.Message) {
	c.channelReactor.AddMessage(m)
}

func (c *channelManager) onSend(m reactor.Message) {
	c.opts.Send(ShardTypeChannel, m)
}

func (c *channelManager) GetConfig(req reactor.ConfigReq) (reactor.ConfigResp, error) {

	channelId, channelType := wkutil.ChannelFromlKey(req.HandlerKey)

	timeoutctx, cancel := context.WithTimeout(c.s.cancelCtx, c.opts.ReqTimeout)
	defer cancel()

	clusterCfg, _, err := c.s.loadOrCreateChannelClusterConfig(timeoutctx, channelId, channelType)
	if err != nil {
		c.Error("get config failed", zap.Error(err))
		return reactor.EmptyConfigResp, err
	}

	var role = replica.RoleUnknown

	if clusterCfg.LeaderId == c.opts.NodeId {
		role = replica.RoleLeader
	} else if wkutil.ArrayContainsUint64(clusterCfg.Learners, c.opts.NodeId) {
		role = replica.RoleLearner
	} else if wkutil.ArrayContainsUint64(clusterCfg.Replicas, c.opts.NodeId) {
		role = replica.RoleFollower
	}
	if role == replica.RoleUnknown {
		c.Error("get config failed, role is unknown", zap.String("cfg", clusterCfg.String()))
		return reactor.EmptyConfigResp, errors.New("role is unknown")
	}

	return reactor.ConfigResp{
		HandlerKey: req.HandlerKey,
		Config: replica.Config{
			MigrateFrom: clusterCfg.MigrateFrom,
			MigrateTo:   clusterCfg.MigrateTo,
			Replicas:    clusterCfg.Replicas,
			Learners:    clusterCfg.Learners,
			Leader:      clusterCfg.LeaderId,
			Term:        clusterCfg.Term,
			Role:        role,
			Version:     clusterCfg.ConfVersion,
		},
	}, nil

}

func (c *channelManager) GetLeaderTermStartIndex(req reactor.LeaderTermStartIndexReq) (uint64, error) {
	reqBytes, err := req.Marshal()
	if err != nil {
		return 0, err
	}
	resp, err := c.request(req.LeaderId, "/channel/leaderTermStartIndex", reqBytes)
	if err != nil {
		return 0, err
	}
	if resp.Status != proto.StatusOK {
		return 0, fmt.Errorf("get leader term start index failed, status: %v", resp.Status)
	}
	if len(resp.Body) > 0 {
		return binary.BigEndian.Uint64(resp.Body), nil
	}
	return 0, nil
}

func (c *channelManager) Append(req reactor.AppendLogReq) error {

	// for _, req := range reqs {
	// 	if req.Logs == nil || len(req.Logs) == 0 {
	// 		continue
	// 	}
	// 	if err := c.opts.MessageLogStorage.AppendLogs(req.HandleKey, req.Logs); err != nil {
	// 		return err
	// 	}

	// }
	return c.opts.MessageLogStorage.Append(req)
}

func (c *channelManager) request(toNodeId uint64, path string, body []byte) (*proto.Response, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), c.opts.ReqTimeout)
	defer cancel()
	return c.s.RequestWithContext(timeoutCtx, toNodeId, path, body)
}
