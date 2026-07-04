package buffer

import (
	"sort"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// DefaultSlabPool provides shared payload buffers for common transport frame sizes.
var DefaultSlabPool = NewSlabPool([]int{512, 4096, 65536, 1048576})

type slabClass struct {
	size int
	pool sync.Pool
}

// SlabPool owns fixed-size payload buffer classes.
type SlabPool struct {
	classes []slabClass
}

// NewSlabPool creates a slab pool from positive unique sizes sorted ascending.
func NewSlabPool(sizes []int) *SlabPool {
	seen := make(map[int]struct{}, len(sizes))
	classes := make([]int, 0, len(sizes))
	for _, size := range sizes {
		if size <= 0 {
			continue
		}
		if _, ok := seen[size]; ok {
			continue
		}
		seen[size] = struct{}{}
		classes = append(classes, size)
	}
	sort.Ints(classes)

	pool := &SlabPool{classes: make([]slabClass, len(classes))}
	for i, size := range classes {
		size := size
		pool.classes[i] = slabClass{
			size: size,
			pool: sync.Pool{
				New: func() any {
					return make([]byte, size)
				},
			},
		}
	}
	return pool
}

// Get returns an owned buffer with length n and capacity from the smallest fitting class.
func (p *SlabPool) Get(n int) core.OwnedBuffer {
	if n <= 0 {
		return core.OwnedBuffer{}
	}
	if p == nil {
		return core.NewOwnedBuffer(make([]byte, n), nil)
	}
	for i := range p.classes {
		class := &p.classes[i]
		if n > class.size {
			continue
		}
		buf, ok := class.pool.Get().([]byte)
		if !ok || cap(buf) < class.size {
			buf = make([]byte, class.size)
		}
		slab := buf[:class.size]
		payload := slab[:n:n]
		return core.NewOwnedBuffer(payload, func([]byte) {
			class.pool.Put(slab)
		})
	}
	return core.NewOwnedBuffer(make([]byte, n), nil)
}
