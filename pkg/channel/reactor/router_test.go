package reactor

import (
	"fmt"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
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

func TestRouterDistributesSequentialPersonChannelsAcrossPowerOfTwoReactors(t *testing.T) {
	for _, reactorCount := range []int{4, 8} {
		t.Run(fmt.Sprintf("reactors_%d", reactorCount), func(t *testing.T) {
			router, err := NewRouter(reactorCount)
			require.NoError(t, err)
			counts := make([]int, reactorCount)
			const pairCount = 10_000
			for pairIndex := 0; pairIndex < pairCount; pairIndex++ {
				leftUID := fmt.Sprintf("cloud-medium-u-%d", pairIndex*2)
				rightUID := fmt.Sprintf("cloud-medium-u-%d", pairIndex*2+1)
				id := ch.ChannelID{
					ID:   channelid.EncodePersonChannel(leftUID, rightUID),
					Type: frame.ChannelTypePerson,
				}
				counts[router.PickIndex(ch.ChannelKeyForID(id))]++
			}

			expected := float64(pairCount) / float64(reactorCount)
			for index, count := range counts {
				require.InDelta(t, expected, count, expected*0.1, "reactor %d count=%d all=%v", index, count, counts)
			}
		})
	}
}

func TestRouterPickIndexDoesNotAllocate(t *testing.T) {
	router, err := NewRouter(4)
	require.NoError(t, err)
	key := ch.ChannelKey("1:cloud-medium-u-0@cloud-medium-u-1")

	require.Zero(t, testing.AllocsPerRun(1_000, func() {
		_ = router.PickIndex(key)
	}))
}
