package gateway

import (
	"fmt"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/stretchr/testify/assert"
)

func TestDataPipeline(t *testing.T) {
	d := newDataPipeline(1)

	s := wkserver.New("tcp://127.0.0.1:0")
	err := s.Start()
	assert.NoError(t, err)
	defer func() {
		err = s.Stop()
		assert.NoError(t, err)
	}()

	content := []byte("test")

	var w sync.WaitGroup
	w.Add(len(content))
	s.Route("/gateway/conn/write", func(c *wkserver.Context) {

		fmt.Println(c.Body())
		err = c.WriteOk()
		assert.NoError(t, err)
		w.Done()

	})

	cli := client.New(s.Addr().String())
	err = cli.Connect()
	assert.NoError(t, err)

	d.nodeClient = cli

	d.start()
	defer d.stop()

	_, err = d.append([]byte(content))
	assert.NoError(t, err)

	w.Wait()

}
