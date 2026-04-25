package app

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
)

type messageRecipientDirectory struct {
	authority presence.Authoritative
}

func (d messageRecipientDirectory) EndpointsByUID(ctx context.Context, uid string) ([]message.Endpoint, error) {
	if d.authority == nil {
		return nil, nil
	}
	routes, err := d.authority.EndpointsByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	endpoints := make([]message.Endpoint, 0, len(routes))
	for _, route := range routes {
		endpoints = append(endpoints, message.Endpoint{
			NodeID:     route.NodeID,
			BootID:     route.BootID,
			SessionID:  route.SessionID,
			DeviceFlag: route.DeviceFlag,
		})
	}
	return endpoints, nil
}

type messageRemoteDelivery struct {
	cluster raftcluster.API
	client  *accessnode.Client
}

func (d messageRemoteDelivery) DeliverRemote(ctx context.Context, cmd message.RemoteDeliveryCommand) error {
	if d.cluster == nil || d.client == nil {
		return nil
	}
	frameBytes, err := codec.New().EncodeFrame(cmd.Frame, 0)
	if err != nil {
		return err
	}
	routes := make([]deliveryruntime.RouteKey, 0, len(cmd.SessionIDs))
	for _, sessionID := range cmd.SessionIDs {
		routes = append(routes, deliveryruntime.RouteKey{
			UID:       cmd.UID,
			NodeID:    cmd.NodeID,
			BootID:    cmd.BootID,
			SessionID: sessionID,
		})
	}
	_, err = d.client.PushBatch(ctx, cmd.NodeID, accessnode.DeliveryPushCommand{
		ChannelID:   cmd.UID,
		ChannelType: 0,
		Routes:      routes,
		Frame:       frameBytes,
	})
	return err
}
