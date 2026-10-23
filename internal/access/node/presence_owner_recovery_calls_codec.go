package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// Version 3 retains complete caller boundaries and an explicit continuation;
// operations 9/10 and WKVO versions 1/2 retain their original byte layouts.
var ownerRouteCallsMagic = []byte{'W', 'K', 'V', 'O', 3}

func appendOwnerRouteCallsRequest(dst []byte, calls []presence.RecoveryCall, start int) ([]byte, error) {
	if !presence.ValidOwnerRecoveryCalls(calls, start) {
		return nil, fmt.Errorf("internal/access/node: owner calls request out of bounds")
	}
	dst = appendUvarint(dst, uint64(start))
	dst = appendUvarint(dst, uint64(len(calls)))
	for _, call := range calls {
		var err error
		dst, err = appendPresenceEndpointLookupGroups(dst, call.Groups)
		if err != nil {
			return nil, err
		}
	}
	return dst, nil
}

// readOwnerRouteCallsRequest enforces aggregate wire budgets before allocating
// nested collections; each original caller retains its independent boundary.
func readOwnerRouteCallsRequest(body []byte, offset int) ([]presence.RecoveryCall, int, int, error) {
	start, offset, err := readUvarint(body, offset)
	if err != nil {
		return nil, 0, offset, err
	}
	count, offset, err := readUvarint(body, offset)
	if err != nil || count == 0 || count > 16 || start >= count {
		return nil, 0, offset, fmt.Errorf("internal/access/node: owner calls count or start out of bounds")
	}
	calls := make([]presence.RecoveryCall, int(count))
	totalGroups, totalUIDs := 0, 0
	for i := range calls {
		groupCount, next, err := readUvarint(body, offset)
		if err != nil || groupCount == 0 || groupCount > uint64(256-totalGroups) {
			return nil, 0, offset, fmt.Errorf("internal/access/node: owner calls groups out of bounds")
		}
		offset = next
		totalGroups += int(groupCount)
		calls[i].Groups = make([]presence.EndpointLookupGroup, int(groupCount))
		for j := range calls[i].Groups {
			group := &calls[i].Groups[j]
			group.Target, offset, err = readPresenceRouteTarget(body, offset)
			if err != nil {
				return nil, 0, offset, err
			}
			uidCount, next, err := readUvarint(body, offset)
			if err != nil || uidCount > uint64(512-totalUIDs) {
				return nil, 0, offset, fmt.Errorf("internal/access/node: owner calls UIDs out of bounds")
			}
			offset = next
			totalUIDs += int(uidCount)
			group.UIDs = make([]string, int(uidCount))
			for k := range group.UIDs {
				group.UIDs[k], offset, err = readString(body, offset)
				if err != nil {
					return nil, 0, offset, err
				}
			}
		}
	}
	if !presence.ValidOwnerRecoveryCalls(calls, int(start)) {
		return nil, 0, offset, fmt.Errorf("internal/access/node: invalid owner calls request")
	}
	return calls, int(start), offset, nil
}

func validOwnerRouteCallPage(page presence.OwnerRouteCallPage, calls []presence.RecoveryCall, start int) bool {
	if !presence.ValidOwnerRecoveryCalls(calls, start) || page.Start != start || page.Next <= start || page.Next > len(calls) || len(page.Results) != page.Next-start {
		return false
	}
	total := 0
	for i, results := range page.Results {
		if len(results) != len(calls[start+i].Groups) {
			return false
		}
		for _, result := range results {
			if len(result.Snapshot.Routes) > maxPresenceRPCCollectionLen-total {
				return false
			}
			total += len(result.Snapshot.Routes)
		}
	}
	return true
}

func encodeOwnerRouteCalls(page presence.OwnerRouteCallPage, calls []presence.RecoveryCall, start int) ([]byte, error) {
	if !validOwnerRouteCallPage(page, calls, start) {
		return nil, fmt.Errorf("internal/access/node: invalid owner calls result page")
	}
	dst := append([]byte(nil), ownerRouteCallsMagic...)
	dst = appendUvarint(dst, uint64(page.Start))
	dst = appendUvarint(dst, uint64(page.Next))
	dst = appendUvarint(dst, uint64(len(page.Results)))
	for _, results := range page.Results {
		dst = appendUvarint(dst, uint64(len(results)))
		for _, result := range results {
			dst = appendString(dst, presenceRPCStatusForError(result.Err))
			dst = appendUvarint(dst, result.Snapshot.OwnerNodeID)
			dst = appendUvarint(dst, result.Snapshot.OwnerBootID)
			dst = appendPresenceRoutes(dst, result.Snapshot.Routes)
		}
	}
	return dst, nil
}

func decodeOwnerRouteCalls(body []byte, calls []presence.RecoveryCall, start int) (presence.OwnerRouteCallPage, error) {
	if !hasMagic(body, ownerRouteCallsMagic) {
		return presence.OwnerRouteCallPage{}, fmt.Errorf("internal/access/node: invalid owner calls response codec")
	}
	echo, offset, err := readUvarint(body, len(ownerRouteCallsMagic))
	if err != nil || echo != uint64(start) {
		return presence.OwnerRouteCallPage{}, fmt.Errorf("internal/access/node: invalid owner calls start echo")
	}
	next, offset, err := readUvarint(body, offset)
	if err != nil || next <= echo || next > uint64(len(calls)) {
		return presence.OwnerRouteCallPage{}, fmt.Errorf("internal/access/node: invalid owner calls continuation")
	}
	count, offset, err := readUvarint(body, offset)
	if err != nil || count != next-echo {
		return presence.OwnerRouteCallPage{}, fmt.Errorf("internal/access/node: misaligned owner calls result count")
	}
	page := presence.OwnerRouteCallPage{Start: start, Next: int(next), Results: make([][]presence.OwnerRouteResult, int(count))}
	total := 0
	for i := range page.Results {
		groups, nextOffset, err := readUvarint(body, offset)
		if err != nil || groups != uint64(len(calls[start+i].Groups)) {
			return presence.OwnerRouteCallPage{}, fmt.Errorf("internal/access/node: misaligned owner calls group count")
		}
		offset = nextOffset
		page.Results[i] = make([]presence.OwnerRouteResult, int(groups))
		for j := range page.Results[i] {
			result, nextOffset, err := readOwnerRouteCallResult(body, offset, maxPresenceRPCCollectionLen-total)
			if err != nil {
				return presence.OwnerRouteCallPage{}, err
			}
			offset = nextOffset
			page.Results[i][j] = result
			total += len(result.Snapshot.Routes)
		}
	}
	if offset != len(body) {
		return presence.OwnerRouteCallPage{}, fmt.Errorf("internal/access/node: trailing owner calls response bytes")
	}
	return page, nil
}

func readOwnerRouteCallResult(body []byte, offset, remaining int) (presence.OwnerRouteResult, int, error) {
	var result presence.OwnerRouteResult
	status, offset, err := readString(body, offset)
	if err != nil {
		return result, offset, err
	}
	switch status {
	case rpcStatusOK, rpcStatusNotLeader, rpcStatusStaleRoute, rpcStatusRouteNotReady,
		rpcStatusContextCanceled, rpcStatusContextDeadlineExceeded, rpcStatusRejected:
	default:
		return result, offset, fmt.Errorf("internal/access/node: invalid owner calls result status")
	}
	result.Err = presenceRPCErrorForStatus(status)
	result.Snapshot.OwnerNodeID, offset, err = readUvarint(body, offset)
	if err != nil {
		return result, offset, err
	}
	result.Snapshot.OwnerBootID, offset, err = readUvarint(body, offset)
	if err != nil {
		return result, offset, err
	}
	count, _, err := readUvarint(body, offset)
	if err != nil || count > uint64(remaining) {
		return result, offset, fmt.Errorf("internal/access/node: owner calls output exceeds limit")
	}
	result.Snapshot.Routes, offset, err = readPresenceRoutes(body, offset)
	return result, offset, err
}
