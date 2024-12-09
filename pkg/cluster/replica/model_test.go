package replica

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLargeLogData(t *testing.T) {
	m := Message{}
	m.MsgType = MsgSyncResp
	m.Logs = []Log{{Index: 1, Term: 1, Data: make([]byte, 1024*1024*10)}}
	data, err := m.Marshal()
	assert.NoError(t, err)

	m2, err := UnmarshalMessage(data)
	assert.NoError(t, err)
	assert.Equal(t, len(m.Logs), len(m2.Logs))

}

func TestReadyState(t *testing.T) {
	r := NewReadyState(2)
	r.StartProcessing()

	t.Run("processFail", func(t *testing.T) {
		assert.Equal(t, true, r.IsProcessing())
		assert.Equal(t, false, r.IsRetry())

		r.ProcessFail()

		assert.Equal(t, true, r.IsProcessing())
		assert.Equal(t, true, r.IsRetry())

		r.Tick()

		assert.Equal(t, true, r.IsProcessing())
		assert.Equal(t, true, r.IsRetry())

		r.Tick()

		assert.Equal(t, false, r.IsProcessing())
		assert.Equal(t, false, r.IsRetry())
	})

	t.Run("processSuccess", func(t *testing.T) {
		r.ProcessSuccess()
		assert.Equal(t, false, r.IsProcessing())
		assert.Equal(t, false, r.IsRetry())
	})

}

func TestReadyTimeoutState(t *testing.T) {
	r := NewReadyTimeoutState("logPrefix", 2, 2, nil)
	r.StartProcessing()

	t.Run("processTimeout", func(t *testing.T) {
		assert.Equal(t, true, r.IsProcessing())
		r.Tick()
		assert.Equal(t, true, r.IsProcessing())
		r.Tick()
		assert.Equal(t, false, r.IsProcessing())
	})
}
