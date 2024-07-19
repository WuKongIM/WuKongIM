package reactor

import "sync"

var handlerPool = sync.Pool{
	New: func() any {
		return &handler{}
	},
}

func getHandlerFromPool() *handler {
	return handlerPool.Get().(*handler)
}
func putHandlerToPool(h *handler) {
	h.reset()
	handlerPool.Put(h)
}
