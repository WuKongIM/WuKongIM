package wkstore

import (
	"bytes"
	"encoding/binary"
	"fmt"
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

	store.StoreMsg("testtopic", []Message{
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
}

func (t *testMessage) GetSeq() uint32 {
	return t.seq
}
func (t *testMessage) GetMessageID() int64 {
	return t.messageID
}

func (t *testMessage) Encode() []byte {
	p := new(bytes.Buffer)
	binary.Write(p, Encoding, MagicNumber)
	binary.Write(p, Encoding, MessageVersion)
	binary.Write(p, Encoding, uint32(len(t.data)))
	binary.Write(p, Encoding, t.seq) // offsetSize = 8
	binary.Write(p, Encoding, int64(0))
	binary.Write(p, Encoding, t.data)
	binary.Write(p, Encoding, EndMagicNumber)
	return p.Bytes()
}

func (t *testMessage) Decode(msg []byte) error {
	offset := 0
	magicNum := msg[offset : len(MagicNumber)+offset]
	if !bytes.Equal(magicNum, MagicNumber[:]) {
		return fmt.Errorf("Start MagicNumber不正确 expect:%s actual:%s", string(MagicNumber[:]), string(magicNum))
	}
	offset += len(MagicNumber)

	// version
	_ = msg[offset]
	offset += len(MessageVersion)

	// dataLen
	dataLen := Encoding.Uint32(msg[offset : MessageDataLenSize+offset])
	offset += MessageDataLenSize

	// seq
	t.seq = uint32(Encoding.Uint64(msg[offset : offset+OffsetSize]))
	offset += OffsetSize

	// applindex
	_ = Encoding.Uint64(msg[offset : offset+AppliIndexSize])
	offset += AppliIndexSize

	// data
	t.data = msg[offset : offset+int(dataLen)]
	offset += int(dataLen)

	// end magic
	endMagicNum := msg[offset : len(EndMagicNumber)+offset]
	if !bytes.Equal(endMagicNum, EndMagicNumber[:]) {
		return fmt.Errorf("End MagicNumber不正确 expect:%s actual:%s", string(EndMagicNumber[:]), string(endMagicNum))
	}
	return nil
}

func (t *testMessage) SetSeq(seq uint32) {
	t.seq = seq
}
