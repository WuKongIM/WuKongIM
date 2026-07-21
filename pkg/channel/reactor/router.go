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
	return int(avalanche64(h.Sum64()) % uint64(r.count))
}

// avalanche64 mixes weak low bits before a power-of-two reactor modulus.
func avalanche64(value uint64) uint64 {
	value ^= value >> 33
	value *= 0xff51afd7ed558ccd
	value ^= value >> 33
	value *= 0xc4ceb9fe1a85ec53
	value ^= value >> 33
	return value
}
