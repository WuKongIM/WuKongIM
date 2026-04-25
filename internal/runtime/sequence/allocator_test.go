package sequence

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryAllocatorAllocatesMonotonicMessageIDs(t *testing.T) {
	seq := &MemoryAllocator{}

	require.Equal(t, int64(1), seq.NextMessageID())
	require.Equal(t, int64(2), seq.NextMessageID())
	require.Equal(t, int64(3), seq.NextMessageID())
}

func TestMemoryAllocatorAllocatesPerChannelSequences(t *testing.T) {
	seq := &MemoryAllocator{}

	require.Equal(t, uint32(1), seq.NextChannelSequence("u1"))
	require.Equal(t, uint32(2), seq.NextChannelSequence("u1"))
	require.Equal(t, uint32(1), seq.NextChannelSequence("u2"))
	require.Equal(t, uint32(3), seq.NextChannelSequence("u1"))
}
