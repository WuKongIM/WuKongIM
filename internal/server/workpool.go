package server

type FrameWorkPool struct {
}

func NewFrameWorkPool() *FrameWorkPool {
	return &FrameWorkPool{}
}

func (f *FrameWorkPool) Submit(task func()) {
	go task()
}
