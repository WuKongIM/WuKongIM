package clusterconfig_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig"
	"github.com/stretchr/testify/assert"
)

func TestServer(t *testing.T) {
	s := clusterconfig.New(1)
	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

}
