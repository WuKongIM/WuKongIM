//go:build integration

package channels

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// TestAppendRouteRetainedHeap measures storage owned by the Channel service,
// without a growing source, runtime, or caller-owned result set.
func TestAppendRouteRetainedHeap(t *testing.T) {
	const channels = 50_000
	const retainedBudget = channels * 300
	ctx := context.Background()
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: uniformRouteSource{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	heap := func() uint64 {
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	baseline := heap()
	for i := range channels {
		id := ch.ChannelID{ID: "retained-route-" + strconv.Itoa(i), Type: 2}
		if route, err := service.ResolveAppendAuthority(ctx, id); err != nil || route.ID != id || route.RouteGeneration != 10 {
			t.Fatalf("resolve %d: generation=%d error=%v", i, route.RouteGeneration, err)
		}
	}
	retained := int64(heap()) - int64(baseline)
	t.Logf("channels=%d retained_heap_bytes=%d budget_bytes=%d", channels, retained, retainedBudget)
	if retained > retainedBudget {
		t.Fatalf("retained heap %d bytes exceeds %d", retained, retainedBudget)
	}
}

// TestAppendRouteHeapPlateaus checks that cold Channel identities stop retaining
// heap even as a long-lived service continues to resolve new Channels.
func TestAppendRouteHeapPlateaus(t *testing.T) {
	const cohortSize = 50_000
	const allowedGrowth = 8 << 20
	ctx := context.Background()
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: uniformRouteSource{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})

	var heap [3]uint64
	for cohort := range heap {
		for i := range cohortSize {
			id := ch.ChannelID{ID: "cold-route-" + strconv.Itoa(cohort*cohortSize+i), Type: 2}
			if route, err := service.ResolveAppendAuthority(ctx, id); err != nil || route.ID != id || route.RouteGeneration != 10 {
				t.Fatalf("cohort %d, resolve %d: generation=%d error=%v", cohort, i, route.RouteGeneration, err)
			}
		}
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		heap[cohort] = stats.HeapAlloc
	}
	runtime.KeepAlive(service)
	growth := int64(heap[2]) - int64(heap[0])
	t.Logf("post_gc_heap=%v growth_bytes=%d allowed_growth_bytes=%d", heap, growth, allowedGrowth)
	if growth > allowedGrowth {
		t.Fatalf("cold route cache retained %d additional bytes after %d new identities", growth, 2*cohortSize)
	}
}

func TestAppendRoutePressurePreservesInFlightVersionFloor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "pressure-fenced", Type: 2}
	source := &delayedRouteSource{id: id, started: make(chan struct{}), resume: make(chan struct{})}
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})

	done := make(chan error, 1)
	go func() {
		_, err := service.ResolveAppendAuthority(ctx, id)
		done <- err
	}()
	select {
	case <-source.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	source.newLeader.Store(true)
	newRoute, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil || newRoute.RouteGeneration != 11 || newRoute.Leader != 2 {
		t.Fatalf("install current authority: generation=%d leader=%d error=%v", newRoute.RouteGeneration, newRoute.Leader, err)
	}
	service.InvalidateAppendAuthority(id, newRoute.Leader, newRoute.Epoch, newRoute.LeaderEpoch, newRoute.RouteGeneration)
	fillColdAppendRoutes(t, ctx, service)
	close(source.resume)
	select {
	case err := <-done:
		if !errors.Is(err, ch.ErrStaleMeta) {
			t.Fatalf("late older result after pressure: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	route, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil || route.RouteGeneration != 11 || route.Leader != 2 {
		t.Fatalf("refresh current authority: generation=%d leader=%d error=%v", route.RouteGeneration, route.Leader, err)
	}
}

func fillColdAppendRoutes(t *testing.T, ctx context.Context, service *Service) {
	t.Helper()
	for i := range 50_000 {
		id := ch.ChannelID{ID: "pressure-" + strconv.Itoa(i), Type: 2}
		if _, err := service.ResolveAppendAuthority(ctx, id); err != nil {
			t.Fatalf("fill cold route %d: %v", i, err)
		}
	}
}

func TestAppendRouteBytePressureRequiresFreshColdApply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "byte-pressure", Type: 2}
	source := &freshRouteSource{id: id}
	runtime := &fakeRuntime{}
	service, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	old, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Few enough identities to fit a count-only cache, with enough key bytes
	// to exercise the byte limit using ordinary route metadata.
	keyPrefix := strings.Repeat("x", 1000)
	for i := range 10_000 {
		if _, err := service.ResolveAppendAuthority(ctx, ch.ChannelID{ID: keyPrefix + strconv.Itoa(i), Type: 2}); err != nil {
			t.Fatalf("fill long route %d: %v", i, err)
		}
	}
	if err := service.ApplyMeta(old); !errors.Is(err, ch.ErrStaleMeta) {
		t.Fatalf("applying evicted stale authority: %v", err)
	}
	if runtime.applyCalls != 0 {
		t.Fatalf("evicted stale authority reached the runtime")
	}
	current := old
	current.RouteGeneration, current.LeaderEpoch, current.Leader = 11, 2, 2
	if err := service.ApplyMeta(current); err != nil {
		t.Fatalf("apply the verified current authority on a cold miss: %v", err)
	}
	fresh, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil || fresh.RouteGeneration != 11 || fresh.Leader != 2 {
		t.Fatalf("reload evicted authority: generation=%d leader=%d error=%v", fresh.RouteGeneration, fresh.Leader, err)
	}
}

type freshRouteSource struct {
	uniformRouteSource
	id      ch.ChannelID
	started chan struct{}
	resume  chan struct{}
}

func TestAppendRouteKeepsLegacySlotReaderUsableUnderPressure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewSlotMetaSource(legacySlotRouteReader{})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	})
	fillColdAppendRoutes(t, ctx, service)
}

func TestAppendRouteKeepsWideSlotIdentityUsableUnderPressure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: NewSlotMetaSource(narrowFreshSlotRouteReader{})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	fillColdAppendRoutes(t, ctx, service)
	id := ch.ChannelID{ID: strings.Repeat("x", 1025), Type: 2}
	if meta, err := service.ResolveAppendAuthority(ctx, id); err != nil || meta.ID != id {
		t.Fatalf("resolve legacy-width Channel after pressure: %v", err)
	}
}

func TestAppendRouteReloadsLargePersistedRoute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, tc := range []struct {
		name       string
		id         string
		fenceBytes int
	}{
		{name: "wide_identity", id: strings.Repeat("x", 60_000), fenceBytes: 17 << 10},
		{name: "oversized_row", id: "large-record", fenceBytes: 17 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := ch.ChannelID{ID: tc.id, Type: 2}
			reader := &largeSlotRouteReader{id: id.ID, fence: strings.Repeat("f", tc.fenceBytes)}
			runtime := &fakeRuntime{}
			service, err := NewService(Config{Runtime: runtime, LocalNode: 1, MetaSource: NewSlotMetaSource(reader, SlotMetaSourceOptions{WideFreshIDs: true})})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			old, err := service.ResolveAppendAuthority(ctx, id)
			if err != nil || len(old.WriteFence.Token) != tc.fenceBytes || old.RouteGeneration != 10 {
				t.Fatalf("prime route: generation=%d fence_bytes=%d error=%v", old.RouteGeneration, len(old.WriteFence.Token), err)
			}
			fillColdAppendRoutes(t, ctx, service)
			reader.changed = true
			if err := service.ApplyMeta(old); !errors.Is(err, ch.ErrStaleMeta) || runtime.applyCalls != 0 {
				t.Fatalf("stale authority after eviction: runtime_calls=%d error=%v", runtime.applyCalls, err)
			}
			current, err := service.ResolveAppendAuthority(ctx, id)
			if err != nil || current.Leader != 2 || current.RouteGeneration != 11 ||
				current.WriteFence.Version != 2 || len(current.WriteFence.Token) != tc.fenceBytes {
				t.Fatalf("reloaded route: leader=%d generation=%d fence_bytes=%d error=%v",
					current.Leader, current.RouteGeneration, len(current.WriteFence.Token), err)
			}
			if reader.freshReads < 2 {
				t.Fatalf("expected fresh reads for both the stale apply and reload, got %d", reader.freshReads)
			}
			if tc.fenceBytes > 16<<20 {
				if err := service.ApplyMeta(current); err != nil {
					t.Fatalf("apply a verified oversized route: %v", err)
				}
				if _, err := service.ResolveAppendAuthority(ctx, id); err != nil || reader.freshReads < 4 {
					t.Fatalf("oversized row must stay cold after the final pin: fresh_reads=%d error=%v", reader.freshReads, err)
				}
			}
		})
	}
}

type largeSlotRouteReader struct {
	legacySlotRouteReader
	id, fence  string
	changed    bool
	freshReads int
}

func (s *largeSlotRouteReader) GetChannelRuntimeMeta(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	meta, err := s.legacySlotRouteReader.GetChannelRuntimeMeta(ctx, id, typ)
	if id == s.id {
		meta.WriteFenceToken, meta.WriteFenceVersion, meta.WriteFenceReason = s.fence, 1, 1
		meta.WriteFenceUntilMS = 10_000
	}
	return meta, err
}

func (s *largeSlotRouteReader) GetChannelRuntimeMetaFresh(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	meta, err := s.GetChannelRuntimeMeta(ctx, id, typ)
	if id == s.id {
		s.freshReads++
		if s.changed {
			meta.Leader, meta.LeaderEpoch, meta.RouteGeneration, meta.WriteFenceVersion = 2, 2, 11, 2
		}
	}
	return meta, err
}

func TestAppendRouteColdReadIgnoresInvalidationOfOlderVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "cold-read-stale-invalidation", Type: 2}
	source := &freshRouteSource{id: id, started: make(chan struct{}), resume: make(chan struct{})}
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	old, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	fillColdAppendRoutes(t, ctx, service)
	done := make(chan error, 1)
	go func() {
		meta, err := service.ResolveAppendAuthority(ctx, id)
		if err == nil && (meta.RouteGeneration != 11 || meta.Leader != 2) {
			err = errors.New("wrong authority after cold reload")
		}
		done <- err
	}()
	select {
	case <-source.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	service.InvalidateAppendAuthority(id, old.Leader, old.Epoch, old.LeaderEpoch, old.RouteGeneration)
	close(source.resume)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("old invalidation blocked a newer cold read: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestAppendRouteKnownInvalidationRejectsEarlierColdRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "known-invalidation-with-pinned-cold-read", Type: 2}
	source := &delayedRefreshSource{id: id, started: make(chan struct{}), resume: make(chan struct{})}
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if _, err := service.ResolveAppendAuthority(ctx, id); err != nil {
		t.Fatal(err)
	}
	fillColdAppendRoutes(t, ctx, service)
	done := make(chan error, 1)
	go func() {
		_, err := service.ResolveAppendAuthority(ctx, id)
		done <- err
	}()
	select {
	case <-source.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	current, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil {
		close(source.resume)
		<-done
		t.Fatal(err)
	}
	service.InvalidateAppendAuthority(id, current.Leader, current.Epoch, current.LeaderEpoch, current.RouteGeneration)
	close(source.resume)
	if err := <-done; !errors.Is(err, ch.ErrStaleMeta) {
		t.Fatalf("cold read started before the newly installed route was invalidated: %v", err)
	}
	refresh, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil || !sameCompleteAppendMeta(current, refresh) {
		t.Fatalf("fresh read after invalidation must validate the current route: %v", err)
	}
}

func TestAppendRouteCanRefreshCurrentVersionWhileOlderReadIsPinned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "cold-read-current-invalidation", Type: 2}
	source := &delayedRefreshSource{id: id, started: make(chan struct{}), resume: make(chan struct{})}
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	current, err := service.ResolveAppendAuthority(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	fillColdAppendRoutes(t, ctx, service)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = service.ResolveAppendAuthority(ctx, id)
	}()
	select {
	case <-source.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	service.InvalidateAppendAuthority(id, current.Leader, current.Epoch, current.LeaderEpoch, current.RouteGeneration)
	meta, err := service.ResolveAppendAuthority(ctx, id)
	close(source.resume)
	<-done
	if err != nil || meta.RouteGeneration != current.RouteGeneration {
		t.Fatalf("fresh read begun after invalidation must revalidate the same current version: %v", err)
	}
}

type delayedRefreshSource struct {
	uniformRouteSource
	id      ch.ChannelID
	started chan struct{}
	resume  chan struct{}
	delayed atomic.Bool
}

func (s *delayedRefreshSource) ResolveChannelMetaFresh(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if id == s.id && s.delayed.CompareAndSwap(false, true) {
		close(s.started)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return ch.Meta{}, ctx.Err()
		}
	}
	return s.ResolveChannelMeta(ctx, id)
}

type legacySlotRouteReader struct{}

func (legacySlotRouteReader) GetChannelRuntimeMeta(_ context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{
		ChannelID: id, ChannelType: typ, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 10,
		Leader: 1, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive),
	}, nil
}

type narrowFreshSlotRouteReader struct{ legacySlotRouteReader }

func (s narrowFreshSlotRouteReader) GetChannelRuntimeMetaFresh(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	if len(id) > 1024 {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrInvalidArgument
	}
	return s.GetChannelRuntimeMeta(ctx, id, typ)
}

func (s *freshRouteSource) ResolveChannelMetaFresh(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	meta, err := s.ResolveChannelMeta(ctx, id)
	if id == s.id {
		meta.RouteGeneration, meta.LeaderEpoch, meta.Leader = 11, 2, 2
		meta.Key = "" // The public metadata interface allows the key to be derived.
		if s.started != nil {
			close(s.started)
			select {
			case <-s.resume:
			case <-ctx.Done():
				return ch.Meta{}, ctx.Err()
			}
		}
	}
	return meta, err
}

type delayedRouteSource struct {
	uniformRouteSource
	id        ch.ChannelID
	started   chan struct{}
	resume    chan struct{}
	newLeader atomic.Bool
}

func (s *delayedRouteSource) ResolveChannelMeta(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	meta, err := s.uniformRouteSource.ResolveChannelMeta(ctx, id)
	if err != nil || id != s.id {
		return meta, err
	}
	if s.newLeader.Load() {
		meta.RouteGeneration, meta.LeaderEpoch, meta.Leader = 11, 2, 2
		return meta, nil
	}
	close(s.started)
	select {
	case <-s.resume:
		return meta, nil
	case <-ctx.Done():
		return ch.Meta{}, ctx.Err()
	}
}

type uniformRouteSource struct{}

func (uniformRouteSource) ResolveChannelMeta(_ context.Context, id ch.ChannelID) (ch.Meta, error) {
	return ch.Meta{
		ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, Leader: 1, RouteGeneration: 10,
		Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive,
	}, nil
}
