package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRatePerSecond(t *testing.T) {
	got, err := ParseRate("5000/s")
	require.NoError(t, err)
	require.Equal(t, 5000.0, got.PerSecond)
}

func TestParseRateRejectsZero(t *testing.T) {
	_, err := ParseRate("0/s")
	require.Error(t, err)
}
