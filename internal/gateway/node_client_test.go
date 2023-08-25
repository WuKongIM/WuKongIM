package gateway

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/stretchr/testify/assert"
)

func TestNodeClientAppend(t *testing.T) {
	s := wkserver.New("tcp://127.0.0.1:0")
	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		err = s.Stop()
		assert.NoError(t, err)
	}()

	var w sync.WaitGroup
	w.Add(1)
	s.Route("/node/conn/write", func(c *wkserver.Context) {
		fmt.Println(string(c.Body()))
		c.WriteOk()
		w.Done()
	})

	opts := newNodeClientOptions()
	addr := strings.ReplaceAll(s.Addr().String(), "tcp://", "")
	opts.addr = addr
	nodecli := newNodeClient(opts)
	err = nodecli.start()
	assert.NoError(t, err)
	defer nodecli.stop()

	_, err = nodecli.append([]byte("test"))
	assert.NoError(t, err)

	w.Wait()
}
