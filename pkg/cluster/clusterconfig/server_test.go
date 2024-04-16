package clusterconfig_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

func TestServer(t *testing.T) {

	logOpts := wklog.NewOptions()
	logOpts.Level = zapcore.DebugLevel
	wklog.Configure(logOpts)

	nk := newTestNetowrk()
	s1 := clusterconfig.New(clusterconfig.NewOptions(
		clusterconfig.WithNodeId(1),
		clusterconfig.WithInitNodes(map[uint64]string{
			1: "127.0.0.1:10001",
			2: "127.0.0.1:10002",
			3: "127.0.0.1:10003",
		}),
		clusterconfig.WithSend(func(m reactor.Message) {
			nk.send(m)
		}),
		clusterconfig.WithConfigPath(t.TempDir()+"/config1.json"),
	),
	)
	s2 := clusterconfig.New(clusterconfig.NewOptions(
		clusterconfig.WithNodeId(2),
		clusterconfig.WithInitNodes(map[uint64]string{
			1: "127.0.0.1:10001",
			2: "127.0.0.1:10002",
			3: "127.0.0.1:10003",
		}),
		clusterconfig.WithSend(func(m reactor.Message) {
			nk.send(m)
		}),
		clusterconfig.WithConfigPath(t.TempDir()+"/config2.json"),
	))
	s3 := clusterconfig.New(clusterconfig.NewOptions(
		clusterconfig.WithNodeId(3),
		clusterconfig.WithInitNodes(map[uint64]string{
			1: "127.0.0.1:10001",
			2: "127.0.0.1:10002",
			3: "127.0.0.1:10003",
		}),
		clusterconfig.WithSend(func(m reactor.Message) {
			nk.send(m)
		}),
		clusterconfig.WithConfigPath(t.TempDir()+"/config3.json"),
	),
	)

	nk.serverMap[1] = s1
	nk.serverMap[2] = s2
	nk.serverMap[3] = s3

	err := s1.Start()
	assert.NoError(t, err)
	err = s2.Start()
	assert.NoError(t, err)
	err = s3.Start()
	assert.NoError(t, err)

	defer s1.Stop()
	defer s2.Stop()
	defer s3.Stop()

	time.Sleep(time.Second * 10)
}

type testNetowrk struct {
	serverMap map[uint64]*clusterconfig.Server
}

func newTestNetowrk() *testNetowrk {
	return &testNetowrk{
		serverMap: make(map[uint64]*clusterconfig.Server),
	}
}

func (t *testNetowrk) send(m reactor.Message) {
	s := t.serverMap[m.To]
	s.AddMessage(m)
}
