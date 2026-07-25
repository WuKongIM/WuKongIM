package cluster

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBackupContinuousChunkCacheAvoidsRepeatedPageLoads(t *testing.T) {
	key, err := backupContinuousChunkKey(backupContinuousChunkKindMessage, BackupMessageLogPageRequest{
		FromSeq: 1, ThroughSeq: 2,
	})
	if err != nil {
		t.Fatalf("backupContinuousChunkKey() error = %v", err)
	}
	cache := &backupContinuousChunkCache{}
	loads := 0
	load := func(context.Context) ([]byte, error) {
		loads++
		return []byte("encoded-page"), nil
	}
	total, first, done, valid, err := cache.chunk(context.Background(), key, 0, 4, load)
	if err != nil {
		t.Fatalf("chunk(first) error = %v", err)
	}
	if total != len("encoded-page") || string(first) != "enco" || done || !valid {
		t.Fatalf("first chunk total=%d body=%q done=%v valid=%v", total, first, done, valid)
	}
	_, second, done, valid, err := cache.chunk(context.Background(), key, 4, 4, load)
	if err != nil {
		t.Fatalf("chunk(second) error = %v", err)
	}
	if string(second) != "ded-" || done || !valid || loads != 1 {
		t.Fatalf("second chunk=%q done=%v valid=%v loads=%d", second, done, valid, loads)
	}
	_, last, done, valid, err := cache.chunk(context.Background(), key, 8, 4, load)
	if err != nil {
		t.Fatalf("chunk(last) error = %v", err)
	}
	if string(last) != "page" || !done || !valid {
		t.Fatalf("last chunk=%q done=%v valid=%v", last, done, valid)
	}
	if _, _, _, _, err := cache.chunk(context.Background(), key, 4, 4, load); err != nil {
		t.Fatalf("chunk(after finish) error = %v", err)
	}
	if loads != 2 {
		t.Fatalf("source loads after finish = %d, want 2", loads)
	}
}

func TestBackupContinuousChunkCacheSerializesConcurrentMaterialization(t *testing.T) {
	firstKey, err := backupContinuousChunkKey(backupContinuousChunkKindMessage, BackupMessageLogPageRequest{
		FromSeq: 1, ThroughSeq: 2,
	})
	if err != nil {
		t.Fatalf("backupContinuousChunkKey(first) error = %v", err)
	}
	secondKey, err := backupContinuousChunkKey(backupContinuousChunkKindMessage, BackupMessageLogPageRequest{
		FromSeq: 3, ThroughSeq: 4,
	})
	if err != nil {
		t.Fatalf("backupContinuousChunkKey(second) error = %v", err)
	}
	cache := &backupContinuousChunkCache{}
	var active atomic.Int32
	var maximum atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	var startedOnce sync.Once
	loads := [2]atomic.Int32{}
	load := func(index int) func(context.Context) ([]byte, error) {
		return func(context.Context) ([]byte, error) {
			loads[index].Add(1)
			current := active.Add(1)
			startedOnce.Do(func() { close(started) })
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			<-release
			active.Add(-1)
			return []byte("encoded-page"), nil
		}
	}
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		if _, _, _, _, err := cache.chunk(context.Background(), firstKey, 0, 4, load(0)); err != nil {
			t.Errorf("chunk(first loader) error = %v", err)
		}
	}()
	<-started
	wait.Add(1)
	go func() {
		defer wait.Done()
		if _, _, _, _, err := cache.chunk(context.Background(), firstKey, 4, 4, load(0)); err != nil {
			t.Errorf("chunk(first waiter) error = %v", err)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		cache.mu.Lock()
		leases := cache.leases
		cache.mu.Unlock()
		if leases == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("same-key waiter did not join the active page lease")
		}
		runtime.Gosched()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		if _, _, _, _, err := cache.chunk(context.Background(), secondKey, 0, 4, load(1)); err != nil {
			t.Errorf("chunk(second key) error = %v", err)
		}
	}()
	close(release)
	wait.Wait()
	if maximum.Load() != 1 {
		t.Fatalf("concurrent materializations = %d, want 1", maximum.Load())
	}
	if loads[0].Load() != 1 || loads[1].Load() != 1 {
		t.Fatalf("materializations by key = %d/%d, want 1/1", loads[0].Load(), loads[1].Load())
	}
}
