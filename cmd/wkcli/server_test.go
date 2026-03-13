package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{"zero", 0, "0 B"},
		{"small", 100, "100 B"},
		{"1023", 1023, "1023 B"},
		{"1KB", 1024, "1.0 KB"},
		{"1.5KB", 1536, "1.5 KB"},
		{"1MB", 1024 * 1024, "1.0 MB"},
		{"100MB", 100 * 1024 * 1024, "100.0 MB"},
		{"1GB", 1024 * 1024 * 1024, "1.0 GB"},
		{"2.5GB", int64(2.5 * 1024 * 1024 * 1024), "2.5 GB"},
		{"1TB", 1024 * 1024 * 1024 * 1024, "1.0 TB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatBytes(tt.bytes))
		})
	}
}
