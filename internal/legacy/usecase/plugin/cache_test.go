package plugin

import (
	"reflect"
	"testing"
	"time"
)

func TestBindingCacheStoresClonesExpiresAndEvictsOldest(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	cache := NewBindingCache(BindingCacheOptions{TTL: time.Minute, MaxEntries: 1, Clock: func() time.Time { return now }})

	cache.Set("u1", []PluginBinding{{UID: "u1", PluginNo: "bot-a"}}, ObservedPlugin{No: "bot-a"}, true)
	bindings, plugin, ok, hit := cache.Get("u1")
	if !hit || !ok || plugin.No != "bot-a" {
		t.Fatalf("cache hit = (%v,%v,%#v), want hit selected bot-a", hit, ok, plugin)
	}
	bindings[0].PluginNo = "mutated"
	bindings, _, _, hit = cache.Get("u1")
	if !hit || !reflect.DeepEqual(bindings, []PluginBinding{{UID: "u1", PluginNo: "bot-a"}}) {
		t.Fatalf("cached bindings mutated: %#v hit=%v", bindings, hit)
	}

	cache.Set("u2", []PluginBinding{{UID: "u2", PluginNo: "bot-b"}}, ObservedPlugin{No: "bot-b"}, true)
	if _, _, _, hit := cache.Get("u1"); hit {
		t.Fatal("u1 cache entry survived max-entry eviction")
	}
	if _, plugin, ok, hit := cache.Get("u2"); !hit || !ok || plugin.No != "bot-b" {
		t.Fatalf("u2 cache = hit %v ok %v plugin %#v", hit, ok, plugin)
	}

	now = now.Add(2 * time.Minute)
	if _, _, _, hit := cache.Get("u2"); hit {
		t.Fatal("expired cache entry hit")
	}
}

func TestBindingCacheDisabledWhenTTLOrCapacityMissing(t *testing.T) {
	cache := NewBindingCache(BindingCacheOptions{})
	cache.Set("u1", []PluginBinding{{UID: "u1", PluginNo: "bot-a"}}, ObservedPlugin{No: "bot-a"}, true)
	if _, _, _, hit := cache.Get("u1"); hit {
		t.Fatal("zero-value cache should be disabled")
	}
}
