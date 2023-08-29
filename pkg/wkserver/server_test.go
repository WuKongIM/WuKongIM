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
		c.Write([]byte("test2"))
	})

	err := s.Start()
	assert.NoError(t, err)
	defer s.Stop()

	cli := client.New(s.Addr().String(), client.WithUID("uid"))
	err = cli.Connect()
	assert.NoError(t, err)
	defer cli.Close()

	resp, err := cli.Request("/test", []byte("test"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("test2"), resp.Body)

	cli.Route("/hi", func(c *client.Context) {
		c.Write([]byte("world"))
	})

	resp, err = s.Request("uid", "/hi", []byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, []byte("world"), resp.Body)
}
