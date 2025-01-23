package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func NewTestServer(t testing.TB, opt ...options.Option) *Server {

	optList := make([]options.Option, 0)
	optList = append(
		optList,
		options.WithRootDir(t.TempDir()),
		options.WithLoggerDir(t.TempDir()),
		options.WithDbShardNum(2),
		options.WithDbSlotShardNum(2),
		options.WithClusterNodeId(1001),
		options.WithClusterSlotCount(5),
		options.WithClusterSlotReplicaCount(1),
		options.WithClusterChannelReplicaCount(1),
		options.WithClusterElectionIntervalTick(10),
		options.WithClusterHeartbeatIntervalTick(1),
		options.WithClusterTickInterval(time.Millisecond*10),
		options.WithClusterChannelReactorSubCount(2),
		options.WithClusterSlotReactorSubCount(2),
	)
	optList = append(optList, opt...)

	opts := options.New(optList...)

	vp := viper.New()
	opts.ConfigureWithViper(vp)
	s := New(opts)

	return s
}

// 创建一个二个节点的分布式服务
func NewTestClusterServerTwoNode(t *testing.T, opt ...options.Option) (*Server, *Server) {

	nodes := make([]*options.Node, 0)

	nodes = append(nodes, &options.Node{
		Id:         1001,
		ServerAddr: "0.0.0.0:11110",
	})

	nodes = append(nodes, &options.Node{
		Id:         1002,
		ServerAddr: "0.0.0.0:11111",
	})

	s1 := NewTestServer(t, options.WithDemoOn(false), options.WithWSAddr("ws://0.0.0.0:5210"), options.WithManagerAddr("0.0.0.0:5310"), options.WithAddr("tcp://0.0.0.0:5110"), options.WithHTTPAddr("0.0.0.0:5001"), options.WithClusterAPIURL("http://127.0.0.1:5001"), options.WithClusterAddr("tcp://0.0.0.0:11110"), options.WithClusterNodeId(1001), options.WithClusterInitNodes(nodes), options.WithOpts(opt...))
	s2 := NewTestServer(t, options.WithDemoOn(false), options.WithWSAddr("ws://0.0.0.0:5220"), options.WithManagerAddr("0.0.0.0:5320"), options.WithAddr("tcp://0.0.0.0:5120"), options.WithHTTPAddr("0.0.0.0:5002"), options.WithClusterAPIURL("http://127.0.0.1:5002"), options.WithClusterAddr("tcp://0.0.0.0:11111"), options.WithClusterNodeId(1002), options.WithClusterInitNodes(nodes), options.WithOpts(opt...))

	return s1, s2
}

func NewTestClusterServerTreeNode(t testing.TB, opt ...options.Option) (*Server, *Server, *Server) {

	nodes := make([]*options.Node, 0)

	nodes = append(nodes, &options.Node{
		Id:         1001,
		ServerAddr: "0.0.0.0:11110",
	}, &options.Node{
		Id:         1002,
		ServerAddr: "0.0.0.0:11111",
	}, &options.Node{
		Id:         1003,
		ServerAddr: "0.0.0.0:11112",
	})

	s1 := NewTestServer(t,
		options.WithDemoOn(false),
		options.WithClusterPongMaxTick(10),
		options.WithClusterSlotReplicaCount(3),
		options.WithClusterChannelReplicaCount(3),
		options.WithWSAddr("ws://0.0.0.0:5210"),
		options.WithManagerAddr("0.0.0.0:5310"),
		options.WithAddr("tcp://0.0.0.0:5110"),
		options.WithHTTPAddr("0.0.0.0:5001"),
		options.WithClusterAPIURL("http://127.0.0.1:5001"),
		options.WithClusterAddr("tcp://0.0.0.0:11110"),
		options.WithClusterNodeId(1001),
		options.WithClusterInitNodes(nodes),
		options.WithClusterTickInterval(time.Millisecond*50),
		options.WithOpts(opt...),
	)

	s2 := NewTestServer(t,
		options.WithDemoOn(false),
		options.WithClusterPongMaxTick(10),
		options.WithClusterSlotReplicaCount(3),
		options.WithClusterChannelReplicaCount(3),
		options.WithWSAddr("ws://0.0.0.0:5220"),
		options.WithManagerAddr("0.0.0.0:5320"),
		options.WithAddr("tcp://0.0.0.0:5120"),
		options.WithHTTPAddr("0.0.0.0:5002"),
		options.WithClusterAPIURL("http://127.0.0.1:5002"),
		options.WithClusterAddr("tcp://0.0.0.0:11111"),
		options.WithClusterNodeId(1002),
		options.WithClusterInitNodes(nodes),
		options.WithClusterTickInterval(time.Millisecond*50),
		options.WithOpts(opt...),
	)

	s3 := NewTestServer(t,
		options.WithDemoOn(false),
		options.WithClusterPongMaxTick(10),
		options.WithClusterSlotReplicaCount(3),
		options.WithClusterChannelReplicaCount(3),
		options.WithWSAddr("ws://0.0.0.0:5230"),
		options.WithManagerAddr("0.0.0.0:5330"),
		options.WithAddr("tcp://0.0.0.0:5130"),
		options.WithHTTPAddr("0.0.0.0:5003"),
		options.WithClusterAPIURL("http://127.0.0.1:5003"),
		options.WithClusterAddr("tcp://0.0.0.0:11112"),
		options.WithClusterNodeId(1003),
		options.WithClusterInitNodes(nodes),
		options.WithClusterTickInterval(time.Millisecond*50),
		options.WithOpts(opt...),
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
			nodes := s.clusterServer.Nodes()
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
		if service.Cluster.LeaderId() == s.opts.Cluster.NodeId {
			return s
		}
	}
	return nil
}

// func TestAddSubscriber(t *testing.T, s *Server, channelId string, channelType uint8, subscribers ...string) {
// 	// 获取u1的最近会话列表
// 	w := httptest.NewRecorder()
// 	req, _ := http.NewRequest("POST", "/channel/subscriber_add", bytes.NewReader([]byte(wkutil.ToJson(map[string]interface{}{
// 		"channel_id":   channelId,
// 		"channel_type": channelType,
// 		"subscribers":  subscribers,
// 	}))))

// 	s.apiServer.r.ServeHTTP(w, req)
// 	assert.Equal(t, http.StatusOK, w.Code)
// }

func TestCreateClient(t *testing.T, s *Server, uid string) *client.Client {
	cli := client.New(s.opts.External.TCPAddr, client.WithUID(uid))
	err := cli.Connect()
	assert.Nil(t, err)
	return cli
}
