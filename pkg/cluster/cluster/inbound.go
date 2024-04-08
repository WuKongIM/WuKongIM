package cluster

import (
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
)

type inbound struct {
	shardQueueMap map[string]*ReplicaMessageQueue
	mu            sync.RWMutex
	opts          *Options
}

func newInbound(opts *Options) *inbound {
	return &inbound{
		shardQueueMap: make(map[string]*ReplicaMessageQueue),
		opts:          opts,
	}
}

func (i *inbound) getShardQueue(shardNo string) *ReplicaMessageQueue {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.shardQueueMap[shardNo]
}

func (i *inbound) addMessage(shardNo string, msg replica.Message) (bool, bool) {
	q := i.getShardQueue(shardNo)
	if q == nil {
		i.mu.Lock()
		q = NewReplicaMessageQueue(i.opts.ReceiveQueueLength, false, i.opts.LazyFreeCycle, i.opts.MaxReceiveQueueSize)
		i.shardQueueMap[shardNo] = q
		i.mu.Unlock()
	}
	trace.GlobalTrace.Metrics.Cluster().InboundFlightMessageCountAdd(1)
	trace.GlobalTrace.Metrics.Cluster().InboundFlightMessageBytesAdd(int64(msg.Size()))
	return q.Add(msg)
}

func (i *inbound) getMessages(shardNo string) []replica.Message {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if q, ok := i.shardQueueMap[shardNo]; ok {
		msgs := q.Get()
		trace.GlobalTrace.Metrics.Cluster().InboundFlightMessageCountAdd(int64(-len(msgs)))
		for _, msg := range msgs {
			trace.GlobalTrace.Metrics.Cluster().InboundFlightMessageBytesAdd(int64(-msg.Size()))
		}
		return msgs
	}
	return nil
}

func (i *inbound) removeShardQueue(shardNo string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	queue := i.shardQueueMap[shardNo]
	if queue != nil {
		msgs := queue.Get()
		if len(msgs) > 0 {
			trace.GlobalTrace.Metrics.Cluster().InboundFlightMessageCountAdd(int64(-len(msgs)))
			for _, msg := range msgs {
				trace.GlobalTrace.Metrics.Cluster().InboundFlightMessageBytesAdd(int64(-msg.Size()))
			}
		}
	}
	delete(i.shardQueueMap, shardNo)
}
