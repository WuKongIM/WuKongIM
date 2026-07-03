package messageid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSnowflakeGeneratorRejectsNodeIDOutsideRange(t *testing.T) {
	_, err := NewSnowflakeGenerator(1024)

	require.Error(t, err)
}

func TestNewSnowflakeGeneratorAllocatesDistinctIDs(t *testing.T) {
	generator, err := NewSnowflakeGenerator(1)
	require.NoError(t, err)

	first := generator.Next()
	second := generator.Next()

	require.NotZero(t, first)
	require.NotZero(t, second)
	require.NotEqual(t, first, second)
}
