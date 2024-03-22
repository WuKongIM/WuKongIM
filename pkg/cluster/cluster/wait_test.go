package cluster

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestCommitWait1(t *testing.T) {
	cm := newCommitWait()

	wait1C, err := cm.addWaitIndex(1)
	assert.NoError(t, err)
	wait2C, err := cm.addWaitIndex(2)
	assert.NoError(t, err)

	cm.commitIndex(1)

	// 验证wait1C被通知
	select {
	case <-wait1C:
	default:
		t.Fatal("wait1C should be notified")
	}

	// 验证waitC2没有被通知
	select {
	case <-wait2C:
		t.Fatal("wait2C should not be notified")
	default:
	}

	// 再测试commit索引2
	cm.commitIndex(2)

	// 验证wait2C被通知
	select {
	case <-wait2C:
	default:
		t.Fatal("wait2C should be notified")
	}
}

func TestCommitWaitNotifyAll(t *testing.T) {
	cm := newCommitWait()

	wait1C, err := cm.addWaitIndex(1)
	assert.NoError(t, err)
	wait2C, err := cm.addWaitIndex(2)
	assert.NoError(t, err)

	cm.commitIndex(10)
	cm.commitIndex(10)

	// 验证wait1C被通知
	select {
	case <-wait1C:
	default:
		t.Fatal("wait1C should be notified")
	}

	// 验证wait2C被通知
	select {
	case <-wait2C:
	default:
		t.Fatal("wait2C should be notified")
	}
}

func TestCommitWait(t *testing.T) {
	cm := newCommitWait()

	var start time.Time

	for i := 0; i < 10000; i++ {

		go func(v uint64) {
			wait2C, _ := cm.addWaitIndex(v)
			<-wait2C
			wklog.Info("wait2C", zap.Uint64("v", v), zap.Duration("cost", time.Since(start)))

		}(uint64(i + 1))
	}

	time.Sleep(time.Second * 2)

	start = time.Now()

	cm.commitIndex(10000)

	time.Sleep(time.Second * 10)

}
