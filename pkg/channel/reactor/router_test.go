package reactor

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestRouterStableAndInBounds(t *testing.T) {
	router, err := NewRouter(4)
	require.NoError(t, err)
	first := router.PickIndex(ch.ChannelKey("1:a"))
	for i := 0; i < 10; i++ {
		require.Equal(t, first, router.PickIndex(ch.ChannelKey("1:a")))
	}
	require.GreaterOrEqual(t, first, 0)
	require.Less(t, first, 4)
}
