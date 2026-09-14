package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// HintPeerCaller is the existing node RPC transport capability.
type HintPeerCaller interface {
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// HintPresence supplies bounded authoritative device routes.
type HintPresence interface {
	EndpointsByUIDs(context.Context, []string) (map[string][]presenceusecase.Route, error)
}

// MessageUpdateHints routes body-free hints and performs exact local writes.
type MessageUpdateHints struct {
	Online   *online.Registry
	Presence HintPresence
	Peers    HintPeerCaller
	NodeID   uint64
}

func (h *MessageUpdateHints) SendMessageUpdateHint(ctx context.Context, uids []string, hint message.MessageUpdateHint) error {
	if len(uids) > 128 {
		return errors.New("message update recipient budget")
	}
	if len(uids) == 0 {
		return nil
	}
	routes, err := h.Presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		return err
	}
	owners := make(map[uint64][]onlinedelivery.Route)
	for _, uid := range uids {
		for _, r := range routes[uid] {
			owners[r.OwnerNodeID] = append(owners[r.OwnerNodeID], onlinedelivery.Route{UID: r.UID, OwnerNodeID: r.OwnerNodeID, OwnerBootID: r.OwnerBootID, OwnerSeq: r.OwnerSeq, SessionID: r.SessionID})
		}
	}
	ids := make([]uint64, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		all := owners[id]
		for start := 0; start < len(all); {
			end := min(start+512, len(all))
			page := all[start:end]
			if id == h.NodeID {
				err = h.WriteMessageUpdateHints(ctx, page, hint)
			} else {
				var body, reply []byte
				body, err = node.EncodeMessageUpdateDelivery(node.MessageUpdateDelivery{Routes: page, Hint: hint})
				for errors.Is(err, node.ErrMessageUpdateFrameBudget) && len(page) > 1 {
					end = start + len(page)/2
					page = all[start:end]
					body, err = node.EncodeMessageUpdateDelivery(node.MessageUpdateDelivery{Routes: page, Hint: hint})
				}
				if errors.Is(err, node.ErrMessageUpdateFrameBudget) {
					// One unusually long identity cannot fit the advisory frame.
					// Drop this hint; normal foreground sync repairs the device.
					start = end
					continue
				}
				if err == nil {
					reply, err = h.Peers.CallRPC(ctx, id, clusternet.RPCMessageUpdateHint, body)
					if err == nil && (len(reply) != 1 || reply[0] != 1) {
						err = errors.New("invalid message update reply")
					}
				}
			}
			if err != nil {
				return err
			}
			start = end
		}
	}
	return nil
}

// WriteMessageUpdateHints never reserves RECVACK state or creates offline work.
func (h *MessageUpdateHints) WriteMessageUpdateHints(ctx context.Context, routes []onlinedelivery.Route, hint message.MessageUpdateHint) error {
	if len(routes) > 512 {
		return errors.New("message update route budget")
	}
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return err
		}
		session, ok := exactLocalSession(h.Online, route)
		if !ok || !session.MessageUpdates {
			continue
		}
		projected := hint
		if hint.ChannelType == 1 {
			left, right, err := runtimechannelid.DecodePersonChannel(hint.ChannelID)
			if err != nil {
				return err
			}
			switch route.UID {
			case left:
				projected.ChannelID = right
			case right:
				projected.ChannelID = left
			default:
				continue
			}
		}
		body, err := json.Marshal(projected)
		if err != nil {
			return err
		}
		// Hints may be dropped on a closed or saturated session; foreground sync is
		// the recovery path, so one slow device must not stall a large group.
		_ = session.Session.WriteDelivery(&frame.EventPacket{Type: "message_updated", Data: body})
	}
	return nil
}
