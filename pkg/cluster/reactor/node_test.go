package reactor

import (
	"testing"
)

func TestHandlerListAdd(t *testing.T) {
	ls := newHandlerList()
	ls.add(&handler{
		key: "test",
	})
}

func TestHandlerListGet(t *testing.T) {
	ls := newHandlerList()
	ls.add(&handler{
		key: "test",
	})
	h := ls.get("test")
	if h == nil {
		t.Fatal("handler is nil")
	}
}

func TestHandlerListRemove(t *testing.T) {
	ls := newHandlerList()
	ls.add(&handler{
		key: "test",
	})
	ls.remove("test")
	h := ls.get("test")
	if h != nil {
		t.Fatal("handler is not nil")
	}
}

func TestHandlerListLen(t *testing.T) {
	ls := newHandlerList()
	ls.add(&handler{
		key: "test",
	})
	ls.add(&handler{
		key: "test",
	})
	ls.add(&handler{
		key: "test",
	})
	if ls.len() != 3 {
		t.Fatal("count is not 3")
	}

}
