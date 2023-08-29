package wkutil

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataPipeline(t *testing.T) {

	content := []byte("test")
	var w sync.WaitGroup
	w.Add(len(content))
	d := NewDataPipeline(1, func(data []byte) error {
		w.Done()
		return nil
	})
	d.Start()
	defer d.Stop()

	_, err := d.Append([]byte(content))
	assert.NoError(t, err)

	w.Wait()

}
