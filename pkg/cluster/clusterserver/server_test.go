package cluster_test

import (
	"testing"
	"time"

	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

func TestServer(t *testing.T) {
	logOpts := wklog.NewOptions()
	logOpts.Level = zapcore.DebugLevel
	wklog.Configure(logOpts)

	s1 := cluster.New(cluster.NewOptions(
		cluster.WithNodeId(1),
		cluster.WithDataDir(t.TempDir()+"/config1"),
		cluster.WithAddr("127.0.0.1:10001"),
		cluster.WithInitNodes(map[uint64]string{
			1: "127.0.0.1:10001",
			2: "127.0.0.1:10002",
			3: "127.0.0.1:10003",
		}),
	))
	s2 := cluster.New(cluster.NewOptions(
		cluster.WithNodeId(2),
		cluster.WithDataDir(t.TempDir()+"test/config2"),
		cluster.WithAddr("127.0.0.1:10002"),
		cluster.WithInitNodes(map[uint64]string{
			1: "127.0.0.1:10001",
			2: "127.0.0.1:10002",
			3: "127.0.0.1:10003",
		}),
	))
	s3 := cluster.New(cluster.NewOptions(
		cluster.WithNodeId(3),
		cluster.WithDataDir(t.TempDir()+"test/config3"),
		cluster.WithAddr("127.0.0.1:10003"),
		cluster.WithInitNodes(map[uint64]string{
			1: "127.0.0.1:10001",
			2: "127.0.0.1:10002",
			3: "127.0.0.1:10003",
		}),
	))

	err := s1.Start()
	assert.NoError(t, err)

	err = s2.Start()
	assert.NoError(t, err)

	err = s3.Start()
	assert.NoError(t, err)

	defer s1.Stop()
	defer s2.Stop()
	defer s3.Stop()

	s1.MustWaitAllSlotsReady(time.Second * 10)
	s2.MustWaitAllSlotsReady(time.Second * 10)
	s3.MustWaitAllSlotsReady(time.Second * 10)

}
