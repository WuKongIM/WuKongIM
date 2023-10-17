package multiraft_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft"
	"github.com/stretchr/testify/assert"
)

func TestMultiRaftStartAndStop(t *testing.T) {
	s := multiraft.New()
	err := s.Start()
	assert.NoError(t, err)

	s.Stop()
}
