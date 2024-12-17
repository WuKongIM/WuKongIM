package reactor

import "github.com/WuKongIM/WuKongIM/internal/reactor"

type ready struct {
	queue       *msgQueue // 消息队列
	offsetIndex uint64    // 当前偏移的下标
	endIndex    uint64    // 当前同步结束的index
}

func newReady(logPrefix string) *ready {

	return &ready{
		queue: newMsgQueue(logPrefix),
	}
}

func (r *ready) sliceAndTruncate() []*reactor.ChannelMessage {
	msgs := r.queue.sliceWithSize(r.offsetIndex+1, r.queue.lastIndex+1, 0)
	if len(msgs) > 0 {
		r.endIndex = msgs[len(msgs)-1].Index
	}
	r.truncate()
	return msgs
}

func (r *ready) truncate() {
	if r.queue.len() == 0 {
		return
	}
	r.queue.truncateTo(r.endIndex + 1)
	r.offsetIndex = r.endIndex
}

func (r *ready) has() bool {

	return r.offsetIndex < r.queue.lastIndex
}

func (r *ready) append(m *reactor.ChannelMessage) {
	m.Index = r.queue.lastIndex + 1
	r.queue.append(m)
}
