package presence

import (
	"container/heap"
	"time"
)

// expiryBucket groups active route identities by their last observed activity second.
type expiryBucket struct {
	// seenUnix is the shared route activity second for every key in this bucket.
	seenUnix int64
	// heapIndex is this bucket's exact position in the authority slot heap.
	heapIndex int
	// keys contains the exact active route identities scheduled in this bucket.
	keys map[identityKey]struct{}
}

// expiryBucketHeap orders activity buckets from oldest to newest.
type expiryBucketHeap []*expiryBucket

func (h expiryBucketHeap) Len() int {
	return len(h)
}

func (h expiryBucketHeap) Less(i, j int) bool {
	return h[i].seenUnix < h[j].seenUnix
}

func (h expiryBucketHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *expiryBucketHeap) Push(value any) {
	bucket := value.(*expiryBucket)
	bucket.heapIndex = len(*h)
	*h = append(*h, bucket)
}

func (h *expiryBucketHeap) Pop() any {
	old := *h
	last := len(old) - 1
	bucket := old[last]
	old[last] = nil
	bucket.heapIndex = -1
	*h = old[:last]
	return bucket
}

func routeSeenUnix(route Route) int64 {
	if route.LastSeenUnix != 0 {
		return route.LastSeenUnix
	}
	return route.ConnectedUnix
}

func (s *authoritySlot) scheduleExpiryLocked(key identityKey, route Route) {
	s.unscheduleExpiryLocked(key)
	seenUnix := routeSeenUnix(route)
	if seenUnix == 0 {
		return
	}
	bucket := s.expiryBySeen[seenUnix]
	if bucket == nil {
		bucket = &expiryBucket{
			seenUnix:  seenUnix,
			heapIndex: -1,
			keys:      make(map[identityKey]struct{}),
		}
		s.expiryBySeen[seenUnix] = bucket
		heap.Push(&s.expiryHeap, bucket)
	}
	bucket.keys[key] = struct{}{}
	s.expiryByKey[key] = bucket
}

func (s *authoritySlot) unscheduleExpiryLocked(key identityKey) {
	bucket := s.expiryByKey[key]
	if bucket == nil {
		return
	}
	delete(s.expiryByKey, key)
	delete(bucket.keys, key)
	if len(bucket.keys) != 0 {
		return
	}
	if current := s.expiryBySeen[bucket.seenUnix]; current == bucket {
		delete(s.expiryBySeen, bucket.seenUnix)
	}
	if bucket.heapIndex >= 0 {
		heap.Remove(&s.expiryHeap, bucket.heapIndex)
	}
}

func (s *authoritySlot) expireLocked(now time.Time, ttl time.Duration) ExpireResult {
	result := ExpireResult{}
	if ttl > 0 && !now.IsZero() {
		for len(s.expiryHeap) > 0 {
			bucket := s.expiryHeap[0]
			if !time.Unix(bucket.seenUnix, 0).Add(ttl).Before(now) {
				break
			}
			heap.Pop(&s.expiryHeap)
			if current := s.expiryBySeen[bucket.seenUnix]; current == bucket {
				delete(s.expiryBySeen, bucket.seenUnix)
			}
			result.DueBuckets++
			for key := range bucket.keys {
				result.Examined++
				if s.expiryByKey[key] != bucket {
					continue
				}
				delete(s.expiryByKey, key)
				route, ok := s.active[key]
				if !ok {
					continue
				}
				s.removeActiveLocked(key, route)
				result.Expired++
			}
		}
	}
	result.IndexRoutes = len(s.expiryByKey)
	result.IndexBuckets = len(s.expiryHeap)
	return result
}
