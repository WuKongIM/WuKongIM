package cluster

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

var _ reactor.IRequest = &channelManager{}

type channelManager struct {
	channelReactor *reactor.Reactor
	opts           *Options
	s              *Server
	wklog.Log
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
		reactor.WithOnHandlerRemove(func(h reactor.IHandler) {
			trace.GlobalTrace.Metrics.Cluster().ChannelActiveCountAdd(-1)
		}),
		reactor.WithRequest(cm),
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
	trace.GlobalTrace.Metrics.Cluster().ChannelActiveCountAdd(1)
	c.channelReactor.AddHandler(ch.key, ch)
}

func (c *channelManager) remove(ch *channel) {
	c.channelReactor.RemoveHandler(ch.key)
}

func (c *channelManager) get(channelId string, channelType uint8) reactor.IHandler {
	return c.channelReactor.Handler(ChannelToKey(channelId, channelType))
}

func (c *channelManager) iterator(f func(h reactor.IHandler) bool) {
	c.channelReactor.IteratorHandler(f)
}

func (c *channelManager) exist(channelId string, channelType uint8) bool {
	return c.channelReactor.ExistHandler(ChannelToKey(channelId, channelType))
}

// 频道数量
func (c *channelManager) channelCount() int {
	return c.channelReactor.HandlerLen()
}

func (c *channelManager) getWithHandleKey(handleKey string) reactor.IHandler {
	return c.channelReactor.Handler(handleKey)
}

func (c *channelManager) proposeAndWait(ctx context.Context, channelId string, channelType uint8, logs []replica.Log) ([]reactor.ProposeResult, error) {
	return c.channelReactor.ProposeAndWait(ctx, ChannelToKey(channelId, channelType), logs)
}

func (c *channelManager) addMessage(m reactor.Message) {
	c.channelReactor.AddMessage(m)
}

func (c *channelManager) onSend(m reactor.Message) {
	c.opts.Send(ShardTypeChannel, m)
}

func (c *channelManager) GetConfig(req reactor.ConfigReq) (reactor.ConfigResp, error) {

	return reactor.EmptyConfigResp, nil
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
	if resp.Status != proto.Status_OK {
		return 0, fmt.Errorf("get leader term start index failed, status: %v", resp.Status)
	}
	if len(resp.Body) > 0 {
		return binary.BigEndian.Uint64(resp.Body), nil
	}
	return 0, nil
}

func (c *channelManager) AppendLogBatch(reqs []reactor.AppendLogReq) error {

	return c.opts.MessageLogStorage.AppendLogBatch(reqs)
}

func (c *channelManager) request(toNodeId uint64, path string, body []byte) (*proto.Response, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), c.opts.ReqTimeout)
	defer cancel()
	return c.s.RequestWithContext(timeoutCtx, toNodeId, path, body)
}
