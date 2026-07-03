package reactor

import (
	"hash/fnv"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// Router maps channel keys to stable reactor indexes.
type Router struct {
	count int
}

// NewRouter creates a stable hash router.
func NewRouter(count int) (Router, error) {
	if count <= 0 {
		return Router{}, ch.ErrInvalidConfig
	}
	return Router{count: count}, nil
}

// PickIndex returns the reactor index for key.
func (r Router) PickIndex(key ch.ChannelKey) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(r.count))
}
