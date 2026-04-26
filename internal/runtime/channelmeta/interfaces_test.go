package channelmeta

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

type compileBootstrapStore struct{}

func (compileBootstrapStore) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, nil
}

func (compileBootstrapStore) UpsertChannelRuntimeMeta(context.Context, metadb.ChannelRuntimeMeta) error {
	return nil
}

type compileRepairStore struct{}

func (compileRepairStore) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, nil
}

func (compileRepairStore) UpsertChannelRuntimeMetaIfLocalLeader(context.Context, metadb.ChannelRuntimeMeta) error {
	return nil
}

type compileChannelObserver struct{}

func (compileChannelObserver) Meta() channel.Meta {
	return channel.Meta{}
}

func (compileChannelObserver) Status() channel.ReplicaState {
	return channel.ReplicaState{}
}

var (
	_ BootstrapStore  = compileBootstrapStore{}
	_ RepairStore     = compileRepairStore{}
	_ ChannelObserver = compileChannelObserver{}
)
