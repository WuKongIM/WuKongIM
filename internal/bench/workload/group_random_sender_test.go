package workload

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRandomOnlineSenderIsStableAcrossCallOrderAndPartitions(t *testing.T) {
	ch := GroupChannel{ChannelIndex: 17, OnlineMembers: []string{"a", "b", "c", "d"}}
	w := &GroupWorkload{cfg: GroupConfig{SenderPick: "random_online", RandomSeed: 42}}
	const count = 128
	want := make([]string, count)
	for i := range want {
		want[i] = w.senderUID(ch, i)
	}
	got := make([]string, count)
	var wg sync.WaitGroup
	for partition := 0; partition < 4; partition++ {
		wg.Add(1)
		go func(partition int) {
			defer wg.Done()
			part := &GroupWorkload{cfg: GroupConfig{TrafficPartitionCount: 4, OwnedTrafficPartitions: []int{partition}}}
			for localOffset := count/4 - 1; localOffset >= 0; localOffset-- {
				index := part.messageIndexForLocalOffset(ch, localOffset)
				got[index] = w.senderUID(ch, index)
			}
		}(partition)
	}
	wg.Wait()
	require.Equal(t, want, got)
	other := ch
	other.ChannelIndex++
	otherSequence := make([]string, count)
	for i := range otherSequence {
		otherSequence[i] = w.senderUID(other, i)
	}
	require.NotEqual(t, want, otherSequence, "different channels should not share a sender sequence")
	require.Empty(t, w.senderUID(GroupChannel{}, 0))
	require.Equal(t, "only", w.senderUID(GroupChannel{OnlineMembers: []string{"only"}}, 0))
}

func BenchmarkRandomOnlineSender(b *testing.B) {
	w := &GroupWorkload{cfg: GroupConfig{SenderPick: "random_online", RandomSeed: 42}}
	ch := GroupChannel{ChannelIndex: 17, OnlineMembers: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = w.senderUID(ch, i)
	}
}
