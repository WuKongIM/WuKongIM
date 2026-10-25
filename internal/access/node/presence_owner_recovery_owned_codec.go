package node

import (
	"context"
	"encoding/binary"
	"errors"
	"unsafe"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

var (
	errOwnedOwnerPage     = errors.New("internal/access/node: malformed bounded owner response")
	errOwnedOwnerRejected = errors.New("internal/access/node: presence rpc rejected")
)

// ownerPagePlan stays on the stack. Its first pass borrows wire bytes without
// cloning strings or allocating result metadata, including legacy responses.
type ownerPagePlan struct {
	start, next, payloadOffset int
	groups                     [16]int
	rows, bytes                uint64
	singleErr                  error
}

type ownerPageCursor struct {
	body   []byte
	offset int
	err    error
}

func (c *ownerPageCursor) unsigned() uint64 {
	if c.err != nil {
		return 0
	}
	value, n := binary.Uvarint(c.body[c.offset:])
	if n <= 0 {
		c.err = errOwnedOwnerPage
		return 0
	}
	c.offset += n
	return value
}

func (c *ownerPageCursor) signed() int64 {
	if c.err != nil {
		return 0
	}
	value, n := binary.Varint(c.body[c.offset:])
	if n <= 0 {
		c.err = errOwnedOwnerPage
		return 0
	}
	c.offset += n
	return value
}

func (c *ownerPageCursor) take(n uint64) []byte {
	if c.err != nil || n > uint64(len(c.body)-c.offset) {
		c.err = errOwnedOwnerPage
		return nil
	}
	start := c.offset
	c.offset += int(n)
	return c.body[start:c.offset]
}

func (c *ownerPageCursor) text() []byte { return c.take(c.unsigned()) }

// status maps borrowed bytes to fixed errors; no per-group error text or raw
// response aliases escape into the decoded ownership ledger.
func (c *ownerPageCursor) status() error {
	switch string(c.text()) {
	case rpcStatusOK:
		return nil
	case rpcStatusNotLeader:
		return authority.ErrNotLeader
	case rpcStatusStaleRoute:
		return authority.ErrStaleRoute
	case rpcStatusRouteNotReady:
		return authority.ErrRouteNotReady
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusRejected:
		return errOwnedOwnerRejected
	default:
		c.err = errOwnedOwnerPage
		return nil
	}
}

func scanOwnerPage(body []byte, version byte, calls []presence.RecoveryCall, start int) (ownerPagePlan, error) {
	p := ownerPagePlan{start: start, next: start + 1}
	if len(body) < 5 || body[0] != 'W' || body[1] != 'K' || body[2] != 'V' || body[3] != 'O' || body[4] != version {
		return p, errOwnedOwnerPage
	}
	c := ownerPageCursor{body: body, offset: 5}
	switch version {
	case 1:
		p.groups[0] = 1
	case 2:
		groups := c.unsigned()
		if groups != uint64(len(calls[0].Groups)) {
			return p, errOwnedOwnerPage
		}
		p.groups[0] = int(groups)
	case 3:
		echo, next, count := c.unsigned(), c.unsigned(), c.unsigned()
		if echo != uint64(start) || next <= echo || next > uint64(len(calls)) || count != next-echo {
			return p, errOwnedOwnerPage
		}
		p.next = int(next)
	default:
		return p, errOwnedOwnerPage
	}
	p.payloadOffset = c.offset
	p.bytes = uint64(p.next-p.start) * uint64(unsafe.Sizeof([]presence.OwnerRouteResult{}))
	for i := 0; i < p.next-p.start; i++ {
		if version == 3 {
			groups := c.unsigned()
			if groups != uint64(len(calls[start+i].Groups)) {
				return p, errOwnedOwnerPage
			}
			p.groups[i] = int(groups)
		}
		p.bytes += uint64(p.groups[i]) * uint64(unsafe.Sizeof(presence.OwnerRouteResult{}))
		for j := 0; j < p.groups[i]; j++ {
			status := c.status()
			if version == 1 {
				p.singleErr = status
			}
			c.unsigned() // owner node
			c.unsigned() // owner boot
			rows := c.unsigned()
			if c.err != nil || rows > 4096-p.rows {
				return p, errOwnedOwnerPage
			}
			p.rows += rows
			p.bytes += rows * uint64(unsafe.Sizeof(presence.Route{}))
			for k := uint64(0); k < rows; k++ {
				p.bytes += uint64(len(c.text())) // independently cloned UID
				for range 4 {
					c.unsigned()
				}
				p.bytes += uint64(len(c.text())) // device ID
				c.take(2)                        // device flag and level
				p.bytes += uint64(len(c.text())) // listener
				c.signed()
				c.signed()
			}
		}
	}
	if c.err != nil || c.offset != len(body) {
		return p, errOwnedOwnerPage
	}
	return p, nil
}

// decodeOwnedOwnerPage reserves every backing and string copy after a complete
// allocation-free scan. Raw wire/request buffers are outside this decoded ledger.
func decodeOwnedOwnerPage(ctx context.Context, body []byte, version byte, calls []presence.RecoveryCall, start int, budget *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error) {
	if err := ctx.Err(); err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	p, err := scanOwnerPage(body, version, calls, start)
	if err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	if p.singleErr != nil {
		return presence.OwnedOwnerRouteCallPage{}, p.singleErr
	}
	lease, err := budget.TryReserve(p.rows, p.bytes)
	if err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	owned := presence.OwnedOwnerRouteCallPage{Page: presence.OwnerRouteCallPage{Start: p.start, Next: p.next, Results: make([][]presence.OwnerRouteResult, p.next-p.start)}, Lease: lease}
	c := ownerPageCursor{body: body, offset: p.payloadOffset}
	for i := range owned.Page.Results {
		if version == 3 {
			c.unsigned()
		} // validated group cardinality
		owned.Page.Results[i] = make([]presence.OwnerRouteResult, p.groups[i])
		for j := range owned.Page.Results[i] {
			result := &owned.Page.Results[i][j]
			result.Err = c.status()
			result.Snapshot.OwnerNodeID = c.unsigned()
			result.Snapshot.OwnerBootID = c.unsigned()
			rows := int(c.unsigned())
			if rows != 0 {
				result.Snapshot.Routes = make([]presence.Route, rows)
			}
			for k := range result.Snapshot.Routes {
				route := &result.Snapshot.Routes[k]
				route.UID = string(c.text())
				route.OwnerNodeID = c.unsigned()
				route.OwnerBootID = c.unsigned()
				route.OwnerSeq = c.unsigned()
				route.SessionID = c.unsigned()
				route.DeviceID = string(c.text())
				flags := c.take(2)
				if len(flags) == 2 {
					route.DeviceFlag, route.DeviceLevel = flags[0], flags[1]
				}
				route.Listener = string(c.text())
				route.ConnectedUnix = c.signed()
				route.LastSeenUnix = c.signed()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		owned.Release()
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	if c.err != nil || c.offset != len(body) {
		owned.Release()
		return presence.OwnedOwnerRouteCallPage{}, errOwnedOwnerPage
	}
	return owned, nil
}
