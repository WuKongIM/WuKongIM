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

func TestParseRateSupportsWhitespaceAndDecimal(t *testing.T) {
	got, err := ParseRate(" 12.5/s ")
	require.NoError(t, err)
	require.Equal(t, 12.5, got.PerSecond)
}

func TestParseRateRejectsInvalidRates(t *testing.T) {
	for _, input := range []string{"0/s", "-1/s", "NaN/s", "+Inf/s", "5000"} {
		t.Run(input, func(t *testing.T) {
			_, err := ParseRate(input)
			require.Error(t, err)
		})
	}
}
