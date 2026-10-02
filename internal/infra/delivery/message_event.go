package delivery

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// MessageEvents maps accepted stream notifications onto presence-routed EVENT
// packets. It retains no payloads, queues, acknowledgments, or offline work.
type MessageEvents struct {
	Online   *online.Registry
	Presence HintPresence
	Peers    HintPeerCaller
	NodeID   uint64
}

func (h *MessageEvents) SendMessageEvent(ctx context.Context, uids []string, event message.MessageEventNotification) error {
	if len(uids) > 128 {
		return errors.New("stream recipient budget")
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
			owners[r.OwnerNodeID] = append(owners[r.OwnerNodeID], onlinedelivery.Route{UID: r.UID, OwnerNodeID: r.OwnerNodeID, OwnerBootID: r.OwnerBootID, OwnerSeq: r.OwnerSeq, SessionID: r.SessionID, DeviceID: r.DeviceID, DeviceFlag: r.DeviceFlag, DeviceLevel: r.DeviceLevel})
		}
	}
	var first error
	for id, all := range owners {
		for start := 0; start < len(all); {
			end := min(start+512, len(all))
			page := all[start:end]
			if id == h.NodeID {
				err = h.WriteMessageEvents(ctx, page, event)
			} else {
				var body, reply []byte
				body, err = node.EncodeMessageEventDelivery(node.MessageEventDelivery{Routes: page, Event: event})
				for errors.Is(err, node.ErrMessageEventFrameBudget) && len(page) > 1 {
					end = start + len(page)/2
					page = all[start:end]
					body, err = node.EncodeMessageEventDelivery(node.MessageEventDelivery{Routes: page, Event: event})
				}
				if err == nil {
					reply, err = h.Peers.CallRPC(ctx, id, clusternet.RPCMessageEventDelivery, body)
					if err == nil && (len(reply) != 1 || reply[0] != 1) {
						err = errors.New("invalid stream event reply")
					}
				}
			}
			if err != nil && first == nil {
				first = err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			start = end
		}
	}
	return first
}

// WriteMessageEvents fences each active owner session. Slow/closed sessions
// drop notifications instead of blocking other recipients; history is recovery.
func (h *MessageEvents) WriteMessageEvents(ctx context.Context, routes []onlinedelivery.Route, event message.MessageEventNotification) error {
	if len(routes) > 512 {
		return errors.New("stream route budget")
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return err
		}
		session, ok := exactLocalSession(h.Online, route)
		if !ok {
			continue
		}
		data := body
		if event.ChannelType == 1 {
			left, right, err := runtimechannelid.DecodePersonChannel(event.ChannelID)
			if err != nil {
				return err
			}
			projected := event
			switch route.UID {
			case left:
				projected.ChannelID = right
			case right:
				projected.ChannelID = left
			default:
				continue
			}
			data, err = json.Marshal(projected)
			if err != nil {
				return err
			}
		}
		_ = session.Session.WriteDelivery(&frame.EventPacket{Id: event.EventID, Type: event.EventType, Timestamp: event.Timestamp, Data: data})
	}
	return nil
}
