package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func NewTestServer(t testing.TB, opt ...Option) *Server {

	optList := make([]Option, 0)
	optList = append(
		optList,
		WithRootDir(t.TempDir()),
		WithLoggerDir(t.TempDir()),
		WithDbShardNum(2),
		WithDbSlotShardNum(2),
		WithClusterNodeId(1001),
		WithClusterSlotCount(5),
		WithClusterSlotReplicaCount(1),
		WithClusterChannelReplicaCount(1),
		WithClusterElectionIntervalTick(10),
		WithClusterHeartbeatIntervalTick(1),
		WithClusterTickInterval(time.Millisecond*10),
		WithClusterChannelReactorSubCount(2),
		WithClusterSlotReactorSubCount(2),
	)
	optList = append(optList, opt...)

	opts := NewOptions(optList...)

	vp := viper.New()
	opts.ConfigureWithViper(vp)
	s := New(opts)

	return s
}

// 创建一个二个节点的分布式服务
func NewTestClusterServerTwoNode(t *testing.T, opt ...Option) (*Server, *Server) {

	nodes := make([]*Node, 0)

	nodes = append(nodes, &Node{
		Id:         1001,
		ServerAddr: "0.0.0.0:11110",
	})

	nodes = append(nodes, &Node{
		Id:         1002,
		ServerAddr: "0.0.0.0:11111",
	})

	s1 := NewTestServer(t, WithDemoOn(false), WithWSAddr("ws://0.0.0.0:5210"), WithManagerAddr("0.0.0.0:5310"), WithAddr("tcp://0.0.0.0:5110"), WithHTTPAddr("0.0.0.0:5001"), WithClusterAPIURL("http://127.0.0.1:5001"), WithClusterAddr("tcp://0.0.0.0:11110"), WithClusterNodeId(1001), WithClusterInitNodes(nodes), WithOpts(opt...))
	s2 := NewTestServer(t, WithDemoOn(false), WithWSAddr("ws://0.0.0.0:5220"), WithManagerAddr("0.0.0.0:5320"), WithAddr("tcp://0.0.0.0:5120"), WithHTTPAddr("0.0.0.0:5002"), WithClusterAPIURL("http://127.0.0.1:5002"), WithClusterAddr("tcp://0.0.0.0:11111"), WithClusterNodeId(1002), WithClusterInitNodes(nodes), WithOpts(opt...))

	return s1, s2
}

func NewTestClusterServerTreeNode(t testing.TB, opt ...Option) (*Server, *Server, *Server) {

	nodes := make([]*Node, 0)

	nodes = append(nodes, &Node{
		Id:         1001,
		ServerAddr: "0.0.0.0:11110",
	}, &Node{
		Id:         1002,
		ServerAddr: "0.0.0.0:11111",
	}, &Node{
		Id:         1003,
		ServerAddr: "0.0.0.0:11112",
	})

	s1 := NewTestServer(t,
		WithDemoOn(false),
		WithClusterPongMaxTick(10),
		WithClusterSlotReplicaCount(3),
		WithClusterChannelReplicaCount(3),
		WithWSAddr("ws://0.0.0.0:5210"),
		WithManagerAddr("0.0.0.0:5310"),
		WithAddr("tcp://0.0.0.0:5110"),
		WithHTTPAddr("0.0.0.0:5001"),
		WithClusterAPIURL("http://127.0.0.1:5001"),
		WithClusterAddr("tcp://0.0.0.0:11110"),
		WithClusterNodeId(1001),
		WithClusterInitNodes(nodes),
		WithClusterTickInterval(time.Millisecond*50),
		WithOpts(opt...),
	)

	s2 := NewTestServer(t,
		WithDemoOn(false),
		WithClusterPongMaxTick(10),
		WithClusterSlotReplicaCount(3),
		WithClusterChannelReplicaCount(3),
		WithWSAddr("ws://0.0.0.0:5220"),
		WithManagerAddr("0.0.0.0:5320"),
		WithAddr("tcp://0.0.0.0:5120"),
		WithHTTPAddr("0.0.0.0:5002"),
		WithClusterAPIURL("http://127.0.0.1:5002"),
		WithClusterAddr("tcp://0.0.0.0:11111"),
		WithClusterNodeId(1002),
		WithClusterInitNodes(nodes),
		WithClusterTickInterval(time.Millisecond*50),
		WithOpts(opt...),
	)

	s3 := NewTestServer(t,
		WithDemoOn(false),
		WithClusterPongMaxTick(10),
		WithClusterSlotReplicaCount(3),
		WithClusterChannelReplicaCount(3),
		WithWSAddr("ws://0.0.0.0:5230"),
		WithManagerAddr("0.0.0.0:5330"),
		WithAddr("tcp://0.0.0.0:5130"),
		WithHTTPAddr("0.0.0.0:5003"),
		WithClusterAPIURL("http://127.0.0.1:5003"),
		WithClusterAddr("tcp://0.0.0.0:11112"),
		WithClusterNodeId(1003),
		WithClusterInitNodes(nodes),
		WithClusterTickInterval(time.Millisecond*50),
		WithOpts(opt...),
	)

	return s1, s2, s3

}

func MustWaitClusterReady(ss ...*Server) {
	for _, s := range ss {
		s.MustWaitClusterReady(time.Second * 10)
	}
}

func TestStartServer(t testing.TB, ss ...*Server) {

	var wait sync.WaitGroup
	for _, s := range ss {
		wait.Add(1)
		go func(sr *Server) {
			err := sr.Start()
			assert.Nil(t, err)
			wait.Done()

		}(s)
	}
	wait.Wait()
}

// MustWaitNodeOffline 必须等待某个节点离线
func (s *Server) MustWaitNodeOffline(nodeId uint64) {
	tk := time.NewTicker(time.Millisecond * 10)
	defer tk.Stop()
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		select {
		case <-tk.C:
			nodes := s.clusterServer.GetConfig().Nodes
			if len(nodes) > 0 {
				for _, node := range nodes {
					if node.Id == nodeId && !node.Online {
						return
					}
				}
			}
		case <-timeoutCtx.Done():
			s.Panic("MustWaitNodeOffline timeout")
			return
		}
	}
}

// 获取领导者节点服务
func GetLeaderServer(ss ...*Server) *Server {
	for _, s := range ss {
		if s.cluster.LeaderId() == s.opts.Cluster.NodeId {
			return s
		}
	}
	return nil
}

func TestAddSubscriber(t *testing.T, s *Server, channelId string, channelType uint8, subscribers ...string) {
	// 获取u1的最近会话列表
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/channel/subscriber_add", bytes.NewReader([]byte(wkutil.ToJson(map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": channelType,
		"subscribers":  subscribers,
	}))))

	s.apiServer.r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCreateClient(t *testing.T, s *Server, uid string) *client.Client {
	cli := client.New(s.opts.External.TCPAddr, client.WithUID(uid))
	err := cli.Connect()
	assert.Nil(t, err)
	return cli
}
