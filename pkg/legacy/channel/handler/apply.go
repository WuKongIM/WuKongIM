package handler

import (
	store "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
)

func ApplyFetch(st *store.ChannelStore, req channel.ApplyFetchStoreRequest) (uint64, error) {
	return st.StoreApplyFetch(req)
}
