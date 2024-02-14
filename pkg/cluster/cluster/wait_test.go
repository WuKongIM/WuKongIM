package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
