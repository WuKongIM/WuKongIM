package multiraft_test

import (
	"os"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/stretchr/testify/assert"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRaft(t *testing.T) {
	opts := multiraft.NewRaftOptions()
	opts.ID = 1
	opts.DataDir = os.TempDir()
	opts.StateMachine = &testStateMachine{}

	s := multiraft.NewRaft(opts)
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	err = s.Campaign()
	assert.NoError(t, err)

	s.HandleReady()

	time.Sleep(time.Second * 1)

}

type testStateMachine struct {
}

func (s *testStateMachine) Apply(enties []raftpb.Entry) error {
	return nil
}
