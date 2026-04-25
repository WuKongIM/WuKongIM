package handler

import (
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func ApplyFetch(st *store.ChannelStore, req channel.ApplyFetchStoreRequest) (uint64, error) {
	return st.StoreApplyFetch(req)
}
