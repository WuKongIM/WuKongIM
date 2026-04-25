package delivery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMailboxRejectsOverDepthAndLeavesActorHealthy(t *testing.T) {
	box := newMailbox(1)

	require.True(t, box.push("first"))
	require.False(t, box.push("second"))
	require.Equal(t, 1, box.depth())
	require.Equal(t, []any{"first"}, box.drain())
	require.Equal(t, 0, box.depth())
	require.True(t, box.push("third"))
}
