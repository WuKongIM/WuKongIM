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
