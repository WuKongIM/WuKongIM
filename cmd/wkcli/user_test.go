package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeviceFlagName(t *testing.T) {
	tests := []struct {
		flag     int
		expected string
	}{
		{0, "APP"},
		{1, "WEB"},
		{2, "PC"},
		{3, "unknown(3)"},
		{-1, "unknown(-1)"},
		{99, "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, deviceFlagName(tt.flag))
		})
	}
}

func TestDeviceLevelName(t *testing.T) {
	tests := []struct {
		level    int
		expected string
	}{
		{0, "slave"},
		{1, "master"},
		{2, "unknown(2)"},
		{-1, "unknown(-1)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, deviceLevelName(tt.level))
		})
	}
}
