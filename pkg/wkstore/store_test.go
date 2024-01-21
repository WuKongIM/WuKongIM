package wkstore

import (
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStoreMsg(t *testing.T) {
	dir, err := ioutil.TempDir("", "filestore")
	assert.NoError(t, err)
	store := NewFileStore(&StoreConfig{
		SlotNum: 1024,
		DataDir: dir,
	})

	store.AppendMessages("testtopic", 1, []Message{
		&testMessage{
			seq:  1,
			data: []byte("test1"),
		},
		&testMessage{
			seq:  1,
			data: []byte("test2"),
		},
	})
}

type testMessage struct {
	seq       uint32
	data      []byte
	messageID int64
	term      uint64
}

func (t *testMessage) GetSeq() uint32 {
	return t.seq
}
func (t *testMessage) GetMessageID() int64 {
	return t.messageID
}

func (t *testMessage) Encode() []byte {

	return EncodeMessage(t.seq, t.term, t.data)
}

func (t *testMessage) SetTerm(term uint64) {
	t.term = term
}

func (t *testMessage) GetTerm() uint64 {
	return t.term
}

func (t *testMessage) Decode(msg []byte) error {

	seq, term, data, err := DecodeMessage(msg)
	if err != nil {
		return err
	}
	t.seq = seq
	t.data = data
	t.term = term
	return nil
}

func (t *testMessage) SetSeq(seq uint32) {
	t.seq = seq
}
