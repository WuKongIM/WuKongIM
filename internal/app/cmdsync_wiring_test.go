package app

import (
	"context"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

// fakeCMDSyncCluster keeps the CMD composition test aligned with its real port.
// Other presence tests deliberately retain their narrower cluster capabilities.
type fakeCMDSyncCluster struct{ *fakePresenceCluster }

var _ clusterinfra.CMDSyncNode = (*fakeCMDSyncCluster)(nil)

func (f *fakeCMDSyncCluster) ReadPermissionMetadataBatchAuthoritative(ctx context.Context, reads []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult {
	rows := make([]slotproxy.PermissionMetadataReadResult, len(reads))
	for i, read := range reads {
		row, err := f.GetChannelMetadataAuthoritative(ctx, read.ChannelID, read.ChannelType)
		rows[i] = slotproxy.PermissionMetadataReadResult{Channel: row, Found: err == nil, Err: err}
	}
	return rows
}
