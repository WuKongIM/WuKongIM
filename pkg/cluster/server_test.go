package cluster_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/assert"
)

func TestServerStartAndStop(t *testing.T) {
	s := cluster.NewServer(1, cluster.WithListenAddr("127.0.0.1:10001"))
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

}

func TestServerWaitLeader(t *testing.T) {
	s1 := cluster.NewServer(1, cluster.WithListenAddr("127.0.0.1:10001"), cluster.WithOnLeaderChange(func(leaderID uint64) {
		fmt.Println("leaderID--change--->", leaderID)
	}))
	err := s1.Start()
	assert.NoError(t, err)
	defer s1.Stop()

	s2 := cluster.NewServer(2, cluster.WithListenAddr("127.0.0.1:10002"), cluster.WithSeed([]string{"127.0.0.1:10001"}), cluster.WithOnLeaderChange(func(leaderID uint64) {
		fmt.Println("leaderID--change--->", leaderID)
	}))
	err = s2.Start()
	assert.NoError(t, err)
	defer s2.Stop()

	// s2.MustWaitLeader(time.Second * 20)
	time.Sleep(time.Second * 20)
}
