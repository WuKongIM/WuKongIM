package reactor

import ch "github.com/WuKongIM/WuKongIM/pkg/channel"

func (r *Reactor) nextOpID() ch.OpID {
	if r.cfg.NextOpID != nil {
		return r.cfg.NextOpID()
	}
	return ch.OpID(r.nextOp.Add(1))
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}
