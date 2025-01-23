package ringlock

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/fasthash"
)

type RingLock struct {
	locker []sync.Mutex
	size   uint32 // 环的大小
}

func NewRingLock(size int) *RingLock {
	locker := make([]sync.Mutex, size)
	return &RingLock{
		locker: locker,
		size:   uint32(size),
	}
}

// 计算哈希值并确定锁的位置
func (r *RingLock) lockPosition(key string) int {
	hash := fasthash.Hash(key)
	position := hash % r.size // 使用哈希值的第一个字节来决定锁的位置
	return int(position)
}

// 获取环型哈希锁
func (r *RingLock) Lock(key string) {
	position := r.lockPosition(key)
	r.locker[position].Lock()
}

// 释放环型哈希锁
func (r *RingLock) Unlock(key string) {
	position := r.lockPosition(key)
	r.locker[position].Unlock()
}
