package server

import "github.com/panjf2000/ants/v2"

type FrameWorkPool struct {
	pool *ants.Pool
}

func NewFrameWorkPool() *FrameWorkPool {
	f := &FrameWorkPool{}

	pool, err := ants.NewPool(5000, ants.WithNonblocking(true))
	if err != nil {
		panic(err)
	}
	f.pool = pool
	return f
}

func (f *FrameWorkPool) Submit(task func()) {
	_ = f.pool.Submit(task)
}
