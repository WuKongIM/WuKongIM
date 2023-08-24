package wkserver_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/stretchr/testify/assert"
)

func TestServerRoute(t *testing.T) {
	s := wkserver.New("tcp://0.0.0.0:0")
	s.Route("/test", func(c *wkserver.Context) {
		err := c.Write([]byte("test2"))
		assert.NoError(t, err)
	})

	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		_ = s.Stop()
	}()

	cli := client.New(s.Addr().String())
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

	resp, err := cli.Request("/test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("test2"), resp.Body)
}
