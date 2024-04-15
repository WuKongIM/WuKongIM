package replica_test

import (
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

func TestElection(t *testing.T) {
	opts := wklog.NewOptions()
	opts.Level = zapcore.DebugLevel
	wklog.Configure(opts)

	var nodeId uint64 = 1
	electionTimeoutTick := 10
	rc := replica.New(nodeId, replica.WithReplicas([]uint64{1, 2, 3}), replica.WithElectionOn(true), replica.WithElectionTimeoutTick(electionTimeoutTick))

	electionWait := sync.WaitGroup{}
	electionWait.Add(1)
	go func() {
		tk := time.NewTicker(time.Millisecond * 10)
		for {
			rd := rc.Ready()
			for _, m := range rd.Messages {
				if m.To == nodeId {
					err := rc.Step(m)
					assert.NoError(t, err)
				} else {
					if m.MsgType == replica.MsgVote {
						err := rc.Step(replica.Message{
							MsgType: replica.MsgVoteResp,
							From:    m.To,
							To:      nodeId,
							Term:    rc.Term(),
						})
						assert.NoError(t, err)
					}
				}
			}
			if rc.IsLeader() {
				electionWait.Done()
			}
			select {
			case <-tk.C:
				rc.Tick()
			}
		}
	}()
	electionWait.Wait()

}
