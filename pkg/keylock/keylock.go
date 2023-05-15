package keylock

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultCleanInterval = 24 * time.Hour //默认24小时清理一次
)

// KeyLock KeyLock
type KeyLock struct {
	locks         map[string]*innerLock //关键字锁map
	cleanInterval time.Duration         //定时清除时间间隔
	stopChan      chan struct{}         //停止信号
	mutex         sync.RWMutex          //全局读写锁
}

// NewKeyLock NewKeyLock
func NewKeyLock() *KeyLock {
	return &KeyLock{
		locks:         make(map[string]*innerLock),
		cleanInterval: defaultCleanInterval,
		stopChan:      make(chan struct{}),
	}
}

//Lock 根据关键字加锁
func (l *KeyLock) Lock(key string) {
	l.mutex.RLock()
	keyLock, ok := l.locks[key]
	if ok {
		keyLock.add()
	}
	l.mutex.RUnlock()
	if !ok {
		l.mutex.Lock()
		keyLock, ok = l.locks[key]
		if !ok {
			keyLock = newInnerLock()
			l.locks[key] = keyLock
		}
		keyLock.add()
		l.mutex.Unlock()
	}
	keyLock.Lock()
}

//Unlock 根据关键字解锁
func (l *KeyLock) Unlock(key string) {
	l.mutex.RLock()
	keyLock, ok := l.locks[key]
	if ok {
		keyLock.done()
	}
	l.mutex.RUnlock()
	if ok {
		keyLock.Unlock()
	}
}

//Clean 清理空闲锁
func (l *KeyLock) Clean() {
	l.mutex.Lock()
	for k, v := range l.locks {
		if v.count == 0 {
			delete(l.locks, k)
		}
	}
	l.mutex.Unlock()
}

//StartCleanLoop 开启清理协程
func (l *KeyLock) StartCleanLoop() {
	go l.cleanLoop()
}

//StopCleanLoop 停止清理协程
func (l *KeyLock) StopCleanLoop() {
	close(l.stopChan)
}

//清理循环
func (l *KeyLock) cleanLoop() {
	ticker := time.NewTicker(l.cleanInterval)
	for {
		select {
		case <-ticker.C:
			l.Clean()
		case <-l.stopChan:
			ticker.Stop()
			return
		}
	}
}

//内部锁信息
type innerLock struct {
	count int64
	sync.Mutex
}

//新建内部锁
func newInnerLock() *innerLock {
	return &innerLock{}
}

func (il *innerLock) add() {
	atomic.AddInt64(&il.count, 1)
}

func (il *innerLock) done() {
	atomic.AddInt64(&il.count, -1)
}
