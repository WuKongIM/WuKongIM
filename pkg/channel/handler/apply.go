package handler

import (
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/db/message"
)

func ApplyFetch(st *store.ChannelStore, req channel.ApplyFetchStoreRequest) (uint64, error) {
	return st.StoreApplyFetch(req)
}
