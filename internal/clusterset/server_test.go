package clusterset_test

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/clusterset"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/stretchr/testify/assert"
)

func TestNewNodeJoin(t *testing.T) {

	// ========== 节点1 ==========
	opts1 := options.New()
	opts1.Mode = options.DebugMode
	opts1.DataDir = path.Join(os.TempDir(), "node1")
	fmt.Println("opts1--->", opts1.DataDir)
	opts1.Cluster.NodeID = 1
	opts1.Cluster.Addr = "tcp://127.0.0.1:11110"
	s1 := clusterset.NewServer(opts1)
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	fmt.Println("start node2")

	// ========== 节点2 ==========

	opts2 := options.New()
	opts2.Mode = options.DebugMode
	opts2.DataDir = path.Join(os.TempDir(), "node2")
	opts2.Cluster.NodeID = 2
	opts2.Cluster.Addr = "tcp://127.0.0.1:11111"
	opts2.Cluster.Join = s1.Addr().String()
	s2 := clusterset.NewServer(opts2)
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	time.Sleep(time.Second * 10)

}
