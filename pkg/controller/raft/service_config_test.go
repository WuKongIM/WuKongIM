package raft

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewServiceValidatesConfig(t *testing.T) {
	service, err := NewService(Config{})
	require.Nil(t, service)
	require.ErrorIs(t, err, ErrInvalidConfig)
}
