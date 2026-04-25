package presence

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type routeKey struct {
	nodeID    uint64
	bootID    uint64
	sessionID uint64
}

type leaseKey struct {
	slotID        uint64
	gatewayNodeID uint64
	gatewayBootID uint64
}

type directory struct {
	mu       sync.RWMutex
	byUID    map[string]map[routeKey]Route
	leases   map[leaseKey]GatewayLease
	ownerSet map[leaseKey]map[routeKey]Route
}

const defaultLeaseTTL = 30 * time.Second

func newDirectory() *directory {
	return &directory{
		byUID:    make(map[string]map[routeKey]Route),
		leases:   make(map[leaseKey]GatewayLease),
		ownerSet: make(map[leaseKey]map[routeKey]Route),
	}
}

func (d *directory) register(slotID uint64, route Route, nowUnix int64) []RouteAction {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweepExpiredLocked(nowUnix)

	k := leaseKey{slotID: slotID, gatewayNodeID: route.NodeID, gatewayBootID: route.BootID}
	if d.byUID[route.UID] == nil {
		d.byUID[route.UID] = make(map[routeKey]Route)
	}
	if d.ownerSet[k] == nil {
		d.ownerSet[k] = make(map[routeKey]Route)
	}

	rk := makeRouteKey(route)
	actions := make([]RouteAction, 0)
	for existingKey, existing := range d.byUID[route.UID] {
		if existingKey == rk {
			continue
		}
		if !conflicts(route, existing) {
			continue
		}
		delete(d.byUID[route.UID], existingKey)
		deleteOwnerRoute(d.ownerSet, d.leases, slotID, existing)
		actions = append(actions, actionForReplacement(route, existing))
	}

	d.byUID[route.UID][rk] = route
	d.ownerSet[k][rk] = route
	leaseUntilUnix := d.leases[k].LeaseUntilUnix
	defaultLeaseUntilUnix := nowUnix + int64(defaultLeaseTTL/time.Second)
	if leaseUntilUnix < defaultLeaseUntilUnix {
		leaseUntilUnix = defaultLeaseUntilUnix
	}
	d.leases[k] = GatewayLease{
		SlotID:         slotID,
		GatewayNodeID:  route.NodeID,
		GatewayBootID:  route.BootID,
		RouteCount:     len(d.ownerSet[k]),
		RouteDigest:    digestRoutes(d.ownerSet[k]),
		LeaseUntilUnix: leaseUntilUnix,
	}

	return actions
}

func (d *directory) unregister(slotID uint64, route Route, nowUnix int64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweepExpiredLocked(nowUnix)

	rk := makeRouteKey(route)
	if routes := d.byUID[route.UID]; routes != nil {
		delete(routes, rk)
		if len(routes) == 0 {
			delete(d.byUID, route.UID)
		}
	}
	deleteOwnerRoute(d.ownerSet, d.leases, slotID, route)
}

func (d *directory) heartbeat(lease GatewayLease, nowUnix int64) HeartbeatAuthoritativeResult {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweepExpiredLocked(nowUnix)

	k := makeLeaseKey(lease)
	current := d.ownerSet[k]
	count := len(current)
	digest := digestRoutes(current)
	mismatch := count != lease.RouteCount || digest != lease.RouteDigest

	lease.RouteCount = count
	lease.RouteDigest = digest
	lease.LeaseUntilUnix = keepLaterLeaseUntil(d.leases[k].LeaseUntilUnix, lease.LeaseUntilUnix)
	d.leases[k] = lease

	return HeartbeatAuthoritativeResult{
		RouteCount:  count,
		RouteDigest: digest,
		Mismatch:    mismatch,
	}
}

func (d *directory) replay(lease GatewayLease, routes []Route, nowUnix int64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweepExpiredLocked(nowUnix)

	k := makeLeaseKey(lease)
	if prev := d.ownerSet[k]; prev != nil {
		for routeKey, route := range prev {
			if byUID := d.byUID[route.UID]; byUID != nil {
				delete(byUID, routeKey)
				if len(byUID) == 0 {
					delete(d.byUID, route.UID)
				}
			}
		}
	}

	nextOwnerSet := make(map[routeKey]Route, len(routes))
	for _, route := range routes {
		if d.replayConflictsLocked(route) {
			continue
		}
		rk := makeRouteKey(route)
		nextOwnerSet[rk] = route
		if d.byUID[route.UID] == nil {
			d.byUID[route.UID] = make(map[routeKey]Route)
		}
		d.byUID[route.UID][rk] = route
	}

	d.ownerSet[k] = nextOwnerSet
	lease.RouteCount = len(nextOwnerSet)
	lease.RouteDigest = digestRoutes(nextOwnerSet)
	lease.LeaseUntilUnix = keepLaterLeaseUntil(d.leases[k].LeaseUntilUnix, lease.LeaseUntilUnix)
	d.leases[k] = lease
}

func (d *directory) endpointsByUID(uid string, nowUnix int64) []Route {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweepExpiredLocked(nowUnix)

	routes := d.byUID[uid]
	if len(routes) == 0 {
		return nil
	}

	out := make([]Route, 0, len(routes))
	for _, route := range routes {
		out = append(out, route)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID != out[j].SessionID {
			return out[i].SessionID < out[j].SessionID
		}
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].BootID < out[j].BootID
	})
	return out
}

func (d *directory) endpointsByUIDs(uids []string, nowUnix int64) map[string][]Route {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweepExpiredLocked(nowUnix)

	out := make(map[string][]Route, len(uids))
	for _, uid := range uids {
		routes := d.byUID[uid]
		if len(routes) == 0 {
			continue
		}

		current := make([]Route, 0, len(routes))
		for _, route := range routes {
			current = append(current, route)
		}
		sort.Slice(current, func(i, j int) bool {
			if current[i].SessionID != current[j].SessionID {
				return current[i].SessionID < current[j].SessionID
			}
			if current[i].NodeID != current[j].NodeID {
				return current[i].NodeID < current[j].NodeID
			}
			return current[i].BootID < current[j].BootID
		})
		out[uid] = current
	}
	return out
}

func (d *directory) sweepExpiredLocked(nowUnix int64) {
	if nowUnix <= 0 {
		return
	}
	for k, lease := range d.leases {
		if lease.LeaseUntilUnix <= 0 || lease.LeaseUntilUnix > nowUnix {
			continue
		}
		d.removeOwnerSetLocked(k)
	}
}

func (d *directory) removeOwnerSetLocked(k leaseKey) {
	routes := d.ownerSet[k]
	for routeKey, route := range routes {
		if byUID := d.byUID[route.UID]; byUID != nil {
			delete(byUID, routeKey)
			if len(byUID) == 0 {
				delete(d.byUID, route.UID)
			}
		}
	}
	delete(d.ownerSet, k)
	delete(d.leases, k)
}

func conflicts(incoming, existing Route) bool {
	if incoming.UID != existing.UID || incoming.DeviceFlag != existing.DeviceFlag {
		return false
	}
	switch incoming.DeviceLevel {
	case uint8(frame.DeviceLevelMaster):
		return true
	case uint8(frame.DeviceLevelSlave):
		return incoming.DeviceID == existing.DeviceID
	default:
		return false
	}
}

func (d *directory) replayConflictsLocked(route Route) bool {
	for _, existing := range d.byUID[route.UID] {
		if conflicts(route, existing) {
			return true
		}
	}
	return false
}

func actionForReplacement(incoming, existing Route) RouteAction {
	kind := "close"
	if incoming.DeviceLevel == uint8(frame.DeviceLevelMaster) && incoming.DeviceID != existing.DeviceID {
		kind = "kick_then_close"
	}
	return RouteAction{
		UID:       existing.UID,
		NodeID:    existing.NodeID,
		BootID:    existing.BootID,
		SessionID: existing.SessionID,
		Kind:      kind,
	}
}

func makeRouteKey(route Route) routeKey {
	return routeKey{
		nodeID:    route.NodeID,
		bootID:    route.BootID,
		sessionID: route.SessionID,
	}
}

func makeLeaseKey(lease GatewayLease) leaseKey {
	return leaseKey{
		slotID:        lease.SlotID,
		gatewayNodeID: lease.GatewayNodeID,
		gatewayBootID: lease.GatewayBootID,
	}
}

func deleteOwnerRoute(ownerSet map[leaseKey]map[routeKey]Route, leases map[leaseKey]GatewayLease, slotID uint64, route Route) {
	k := leaseKey{slotID: slotID, gatewayNodeID: route.NodeID, gatewayBootID: route.BootID}
	routes := ownerSet[k]
	rk := makeRouteKey(route)
	if routes == nil || routes[rk].UID == "" {
		for candidateKey, candidateRoutes := range ownerSet {
			if _, ok := candidateRoutes[rk]; !ok {
				continue
			}
			k = candidateKey
			routes = candidateRoutes
			break
		}
	}
	if routes == nil {
		return
	}
	delete(routes, rk)
	if len(routes) == 0 {
		delete(ownerSet, k)
		delete(leases, k)
		return
	}
}

func digestRoutes(routes map[routeKey]Route) uint64 {
	var digest uint64
	for _, route := range routes {
		digest ^= routeFingerprint(route)
	}
	return digest
}

// Keep this fingerprint aligned with online.SlotSnapshot.Digest. The owner set
// already scopes routes by gateway node/boot, so those fields are excluded.
func routeFingerprint(route Route) uint64 {
	h := fnv.New64a()
	writeUint64(h, route.SessionID)
	writeString(h, route.UID)
	writeString(h, route.DeviceID)
	writeUint64(h, uint64(route.DeviceFlag))
	writeUint64(h, uint64(route.DeviceLevel))
	writeString(h, route.Listener)
	return h.Sum64()
}

func writeUint64(h hash.Hash64, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeString(h hash.Hash64, value string) {
	_, _ = h.Write([]byte(value))
	_, _ = h.Write([]byte{0})
}

func keepLaterLeaseUntil(current, next int64) int64 {
	if next < current {
		return current
	}
	return next
}
