package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChannelTypeName(t *testing.T) {
	tests := []struct {
		typ      int
		expected string
	}{
		{1, "group"},
		{2, "person"},
		{3, "live"},
		{4, "temp"},
		{5, "agent"},
		{0, "unknown(0)"},
		{6, "unknown(6)"},
		{-1, "unknown(-1)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, channelTypeName(tt.typ))
		})
	}
}
