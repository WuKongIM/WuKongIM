package channelid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodePersonChannelBreaksCRCTiesDeterministically(t *testing.T) {
	left := "l98cu"
	right := "pvdba"

	require.Equal(t, EncodePersonChannel(left, right), EncodePersonChannel(right, left))
}
