package app

import (
	"context"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internal/legacy/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
)

type managementConnectionReader struct {
	nodeClient *accessnode.Client
}

func (r managementConnectionReader) NodeConnections(ctx context.Context, nodeID uint64) ([]managementusecase.Connection, error) {
	if r.nodeClient == nil {
		return nil, fmt.Errorf("app: node client not configured")
	}
	items, err := r.nodeClient.Connections(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	out := make([]managementusecase.Connection, 0, len(items))
	for _, item := range items {
		out = append(out, toManagementConnection(item))
	}
	return out, nil
}

func (r managementConnectionReader) NodeConnection(ctx context.Context, nodeID uint64, sessionID uint64) (managementusecase.ConnectionDetail, error) {
	if r.nodeClient == nil {
		return managementusecase.ConnectionDetail{}, fmt.Errorf("app: node client not configured")
	}
	item, err := r.nodeClient.Connection(ctx, nodeID, sessionID)
	if err != nil {
		return managementusecase.ConnectionDetail{}, err
	}
	return toManagementConnection(item), nil
}

func toManagementConnection(item accessnode.Connection) managementusecase.Connection {
	return managementusecase.Connection{
		NodeID:      item.NodeID,
		SessionID:   item.SessionID,
		UID:         item.UID,
		DeviceID:    item.DeviceID,
		DeviceFlag:  item.DeviceFlag,
		DeviceLevel: item.DeviceLevel,
		SlotID:      item.SlotID,
		State:       item.State,
		Listener:    item.Listener,
		ConnectedAt: item.ConnectedAt,
		RemoteAddr:  item.RemoteAddr,
		LocalAddr:   item.LocalAddr,
	}
}
