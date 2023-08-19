package wait

import (
	"hash/crc32"
	"log"
	"sync"
)

const (
	defaultListElementLength = 64
)

type Wait interface {
	// Register waits returns a chan that waits on the given ID.
	// The chan will be triggered when Trigger is called with
	// the same ID.
	Register(id string) <-chan interface{}
	// Trigger triggers the waiting chans with the given ID.
	Trigger(id string, x interface{})
	IsRegistered(id string) bool
}

type listElement struct {
	l sync.RWMutex
	m map[string]chan interface{}
}

type list struct {
	e []listElement
}

// New creates a Wait.
func New() Wait {
	res := list{
		e: make([]listElement, defaultListElementLength),
	}
	for i := 0; i < len(res.e); i++ {
		res.e[i].m = make(map[string]chan interface{})
	}
	return &res
}

func (w *list) Register(id string) <-chan interface{} {
	idx := w.strToNum(id)
	newCh := make(chan interface{}, 1)
	w.e[idx].l.Lock()
	defer w.e[idx].l.Unlock()
	if _, ok := w.e[idx].m[id]; !ok {
		w.e[idx].m[id] = newCh
	} else {
		log.Panicf("dup id %x", id)
	}
	return newCh
}

func (w *list) Trigger(id string, x interface{}) {
	idx := w.strToNum(id)
	w.e[idx].l.Lock()
	ch := w.e[idx].m[id]
	delete(w.e[idx].m, id)
	w.e[idx].l.Unlock()
	if ch != nil {
		ch <- x
		close(ch)
	}
}
func (w *list) IsRegistered(id string) bool {
	idx := w.strToNum(id)
	w.e[idx].l.RLock()
	defer w.e[idx].l.RUnlock()
	_, ok := w.e[idx].m[id]
	return ok
}

func (w *list) strToNum(id string) uint32 {
	return crc32.ChecksumIEEE([]byte(id)) % defaultListElementLength
}
