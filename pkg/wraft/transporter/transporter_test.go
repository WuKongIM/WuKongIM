package transporter_test

import (
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wraft/transporter"
	"github.com/stretchr/testify/assert"
)

func TestTransporterAuth(t *testing.T) {
	var wait sync.WaitGroup
	wait.Add(1)
	recvChan := make(chan []byte, 0)
	tran := transporter.New(1, "tcp://0.0.0.0:0", recvChan, transporter.WithToken("1234"))
	err := tran.Start()
	assert.NoError(t, err)

	go func() {
		for data := range recvChan {
			if string(data) == "hello" {
				wait.Done()
			}
		}
	}()

	defer tran.Stop()

	cli := transporter.NewNodeClient(2, tran.Addr().String(), "1234")
	err = cli.Connect()
	assert.NoError(t, err)

	err = cli.Send([]byte("hello"))
	assert.NoError(t, err)

	wait.Wait()

}
