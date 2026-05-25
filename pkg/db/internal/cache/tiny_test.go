package cache_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/cache"
)

func TestTinyCacheSetGetDelete(t *testing.T) {
	c := cache.NewTiny[string, int](2)
	c.Set("a", 1)
	got, ok := c.Get("a")
	if !ok || got != 1 {
		t.Fatalf("Get() = %d ok=%v, want 1 true", got, ok)
	}
	c.Delete("a")
	if _, ok := c.Get("a"); ok {
		t.Fatal("key still exists after delete")
	}
}

func TestTinyCacheBoundsCapacity(t *testing.T) {
	c := cache.NewTiny[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	if c.Len() > 2 {
		t.Fatalf("Len() = %d, want <= 2", c.Len())
	}
}

func TestTinyCacheClear(t *testing.T) {
	c := cache.NewTiny[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", c.Len())
	}
}

func TestTinyCacheZeroCapacityDoesNotStore(t *testing.T) {
	c := cache.NewTiny[string, int](0)
	c.Set("a", 1)
	if _, ok := c.Get("a"); ok {
		t.Fatal("zero-capacity cache stored a value")
	}
}
