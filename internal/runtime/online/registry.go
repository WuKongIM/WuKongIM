package online

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
	"reflect"
	"sort"
	"sync"
)

type MemoryRegistry struct {
	mu        sync.RWMutex
	bySession map[uint64]OnlineConn
	byUID     map[string]map[uint64]OnlineConn
	byGroup   map[uint64]*groupBucket
}

type groupBucket struct {
	conns  map[uint64]OnlineConn
	count  int
	digest uint64
}

func NewRegistry() *MemoryRegistry {
	return &MemoryRegistry{
		bySession: make(map[uint64]OnlineConn),
		byUID:     make(map[string]map[uint64]OnlineConn),
		byGroup:   make(map[uint64]*groupBucket),
	}
}

func (r *MemoryRegistry) Register(conn OnlineConn) error {
	if r == nil {
		return nil
	}
	if conn.SessionID == 0 || conn.UID == "" || isNilSession(conn.Session) {
		return ErrInvalidConnection
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.bySession == nil {
		r.bySession = make(map[uint64]OnlineConn)
	}
	if r.byUID == nil {
		r.byUID = make(map[string]map[uint64]OnlineConn)
	}
	if r.byGroup == nil {
		r.byGroup = make(map[uint64]*groupBucket)
	}

	if existing, ok := r.bySession[conn.SessionID]; ok {
		r.removeActiveIndexes(existing)
	}

	if conn.State == 0 {
		conn.State = LocalRouteStateActive
	}
	r.bySession[conn.SessionID] = conn
	if conn.State != LocalRouteStateClosing {
		r.addActiveIndexes(conn)
	}
	return nil
}

func (r *MemoryRegistry) Unregister(sessionID uint64) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	conn, ok := r.bySession[sessionID]
	if !ok {
		return
	}

	delete(r.bySession, sessionID)
	r.removeActiveIndexes(conn)
}

func (r *MemoryRegistry) MarkClosing(sessionID uint64) (OnlineConn, bool) {
	if r == nil {
		return OnlineConn{}, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	conn, ok := r.bySession[sessionID]
	if !ok {
		return OnlineConn{}, false
	}

	if conn.State != LocalRouteStateClosing {
		r.removeActiveIndexes(conn)
		conn.State = LocalRouteStateClosing
		r.bySession[sessionID] = conn
	}
	return conn, true
}

func (r *MemoryRegistry) Connection(sessionID uint64) (OnlineConn, bool) {
	if r == nil {
		return OnlineConn{}, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	conn, ok := r.bySession[sessionID]
	return conn, ok
}

func (r *MemoryRegistry) ConnectionsByUID(uid string) []OnlineConn {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	conns := r.byUID[uid]
	if len(conns) == 0 {
		return nil
	}

	out := make([]OnlineConn, 0, len(conns))
	for _, conn := range conns {
		out = append(out, conn)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

func (r *MemoryRegistry) ActiveConnectionsBySlot(slotID uint64) []OnlineConn {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	conns := r.byGroup[slotID]
	if conns == nil || conns.count == 0 {
		return nil
	}

	out := make([]OnlineConn, 0, conns.count)
	for _, conn := range conns.conns {
		out = append(out, conn)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

func (r *MemoryRegistry) ActiveSlots() []SlotSnapshot {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]SlotSnapshot, 0, len(r.byGroup))
	for slotID, bucket := range r.byGroup {
		if bucket == nil || bucket.count == 0 {
			continue
		}
		snapshot := SlotSnapshot{
			SlotID: slotID,
			Count:  bucket.count,
			Digest: bucket.digest,
		}
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SlotID < out[j].SlotID
	})
	return out
}

func (r *MemoryRegistry) addActiveIndexes(conn OnlineConn) {
	if _, ok := r.byUID[conn.UID]; !ok {
		r.byUID[conn.UID] = make(map[uint64]OnlineConn)
	}
	r.byUID[conn.UID][conn.SessionID] = conn

	bucket, ok := r.byGroup[conn.SlotID]
	if !ok || bucket == nil {
		bucket = &groupBucket{conns: make(map[uint64]OnlineConn)}
		r.byGroup[conn.SlotID] = bucket
	}
	if bucket.conns == nil {
		bucket.conns = make(map[uint64]OnlineConn)
	}
	bucket.conns[conn.SessionID] = conn
	bucket.count++
	bucket.digest ^= routeFingerprint(conn)
}

func (r *MemoryRegistry) removeActiveIndexes(conn OnlineConn) {
	if sessions, ok := r.byUID[conn.UID]; ok {
		delete(sessions, conn.SessionID)
		if len(sessions) == 0 {
			delete(r.byUID, conn.UID)
		}
	}
	if bucket, ok := r.byGroup[conn.SlotID]; ok && bucket != nil {
		if _, exists := bucket.conns[conn.SessionID]; exists {
			delete(bucket.conns, conn.SessionID)
			bucket.count--
			bucket.digest ^= routeFingerprint(conn)
			if bucket.count == 0 {
				delete(r.byGroup, conn.SlotID)
			}
		}
	}
}

func routeFingerprint(conn OnlineConn) uint64 {
	h := fnv.New64a()
	writeUint64(h, conn.SessionID)
	writeString(h, conn.UID)
	writeString(h, conn.DeviceID)
	writeUint64(h, uint64(conn.DeviceFlag))
	writeUint64(h, uint64(conn.DeviceLevel))
	writeString(h, conn.Listener)
	return h.Sum64()
}

func writeUint64(h hash.Hash64, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	_, _ = h.Write(b[:])
}

func writeString(h hash.Hash64, s string) {
	_, _ = h.Write([]byte(s))
	_, _ = h.Write([]byte{0})
}

func isNilSession(sess any) bool {
	if sess == nil {
		return true
	}

	rv := reflect.ValueOf(sess)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
