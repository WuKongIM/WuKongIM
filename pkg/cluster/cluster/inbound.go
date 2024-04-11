package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type inbound struct {
	shardQueueMap []map[string]*ReplicaMessageQueue
	mu            sync.RWMutex
	opts          *Options
	shardNum      uint32
}

func newInbound(opts *Options) *inbound {
	var shardNum uint32 = 16
	shardQueueMaps := make([]map[string]*ReplicaMessageQueue, shardNum)
	for i := 0; i < int(shardNum); i++ {
		shardQueueMaps[i] = make(map[string]*ReplicaMessageQueue)
	}
	return &inbound{
		shardQueueMap: shardQueueMaps,
		opts:          opts,
		shardNum:      shardNum,
	}
}

func (i *inbound) getShardQueue(shardNo string, shardId uint32) *ReplicaMessageQueue {
	i.mu.RLock()
	defer i.mu.RUnlock()
	shard := shardId % i.shardNum
	return i.shardQueueMap[shard][shardNo]
}

func (i *inbound) setShardQueue(shardNo string, shardId uint32, q *ReplicaMessageQueue) {
	i.mu.Lock()
	defer i.mu.Unlock()
	shard := shardId % i.shardNum
	i.shardQueueMap[shard][shardNo] = q
}

func (i *inbound) addMessage(shardNo string, shardId uint32, msg replica.Message) (bool, bool) {
	q := i.getShardQueue(shardNo, shardId)
	if q == nil {
		q = NewReplicaMessageQueue(i.opts.ReceiveQueueLength, false, i.opts.LazyFreeCycle, i.opts.MaxReceiveQueueSize)
		i.setShardQueue(shardNo, shardId, q)
	}
	return q.Add(msg)
}

func (i *inbound) getMessages(shardNo string, shardId uint32) []replica.Message {
	q := i.getShardQueue(shardNo, shardId)
	if q != nil {
		return q.Get()
	}
	return nil
}

func (i *inbound) removeShardQueue(shardNo string, shardId uint32) {
	i.mu.Lock()
	defer i.mu.Unlock()
	shard := shardId % i.shardNum
	delete(i.shardQueueMap[shard], shardNo)
}

func (i *inbound) Len() uint64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var len uint64 = 0
	for _, mp := range i.shardQueueMap {
		for _, q := range mp {
			len += q.Len()
		}
	}
	return len
}

func (i *inbound) Size() uint64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	var size uint64 = 0
	for _, mp := range i.shardQueueMap {
		for _, q := range mp {
			size += q.Size()
		}
	}
	return size
}
