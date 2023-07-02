package wkstore

import (
	"io/ioutil"
	"path"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/stretchr/testify/assert"
)

func TestStreamAppenItem(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "stream")
	assert.NoError(t, err)

	st := NewStream("1", path.Join(tmpDir, ""), &StoreConfig{
		DataDir: tmpDir,
	})
	streamSeq, err := st.appendItem(&StreamItem{
		ClientMsgNo: "cli1",
		Blob:        []byte("b1 b2 b3 b4 b5"),
	})
	assert.NoError(t, err)
	assert.Equal(t, uint32(1), streamSeq)

	streamSeq, err = st.appendItem(&StreamItem{
		ClientMsgNo: "cli2",
		Blob:        []byte("b1 b2 b3 b4 b5"),
	})
	assert.NoError(t, err)
	assert.Equal(t, uint32(2), streamSeq)
}

func TestStreamRead(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "stream")
	assert.NoError(t, err)

	st := NewStream("1", path.Join(tmpDir, ""), &StoreConfig{
		DataDir: tmpDir,
	})
	streamSeq, err := st.appendItem(&StreamItem{
		ClientMsgNo: "cli1",
		Blob:        []byte("hello"),
	})
	assert.NoError(t, err)
	assert.Equal(t, uint32(1), streamSeq)

	streamSeq, err = st.appendItem(&StreamItem{
		ClientMsgNo: "cli2",
		Blob:        []byte("world"),
	})
	assert.NoError(t, err)
	assert.Equal(t, uint32(2), streamSeq)

	items, err := st.readItems()
	assert.NoError(t, err)
	assert.Equal(t, 2, len(items))
	assert.Equal(t, "cli1", items[0].ClientMsgNo)
	assert.Equal(t, "cli2", items[1].ClientMsgNo)

	assert.Equal(t, uint32(1), items[0].StreamSeq)
	assert.Equal(t, uint32(2), items[1].StreamSeq)

	assert.Equal(t, []byte("hello"), items[0].Blob)
	assert.Equal(t, []byte("world"), items[1].Blob)

}

func TestStreamMeta(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "stream")
	assert.NoError(t, err)
	st := NewStream("1", path.Join(tmpDir, ""), &StoreConfig{
		DataDir: tmpDir,
	})

	// ----------------- test saveMeta first -----------------
	err = st.saveMeta(&StreamMeta{
		StreamNo:    "1",
		MessageID:   100,
		ChannelID:   "c1",
		ChannelType: 1,
		MessageSeq:  2,
		StreamFlag:  wkproto.StreamFlagStart,
	})
	assert.NoError(t, err)

	meta, err := st.readMeta()
	assert.NoError(t, err)

	assert.Equal(t, "1", meta.StreamNo)
	assert.Equal(t, int64(100), meta.MessageID)
	assert.Equal(t, "c1", meta.ChannelID)
	assert.Equal(t, uint8(1), meta.ChannelType)
	assert.Equal(t, uint32(2), meta.MessageSeq)
	assert.Equal(t, wkproto.StreamFlagStart, meta.StreamFlag)

	// ----------------- test saveMeta second -----------------
	err = st.saveMeta(&StreamMeta{
		StreamNo:    "1",
		MessageID:   23999,
		ChannelID:   "c1293923",
		ChannelType: 2,
		MessageSeq:  293293,
		StreamFlag:  wkproto.StreamFlagStart,
	})
	assert.NoError(t, err)

	meta, err = st.readMeta()
	assert.NoError(t, err)

	assert.Equal(t, "1", meta.StreamNo)
	assert.Equal(t, int64(23999), meta.MessageID)
	assert.Equal(t, "c1293923", meta.ChannelID)
	assert.Equal(t, uint8(2), meta.ChannelType)
	assert.Equal(t, uint32(293293), meta.MessageSeq)
	assert.Equal(t, wkproto.StreamFlagStart, meta.StreamFlag)
}

func TestStreamEnd(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "stream")
	assert.NoError(t, err)
	st := NewStream("1", path.Join(tmpDir, ""), &StoreConfig{
		DataDir: tmpDir,
	})

	err = st.saveMeta(&StreamMeta{
		StreamNo:    "1",
		MessageID:   100,
		ChannelID:   "c1",
		ChannelType: 1,
		MessageSeq:  2,
		StreamFlag:  wkproto.StreamFlagStart,
	})
	assert.NoError(t, err)

	err = st.streamEnd()
	assert.NoError(t, err)

	meta, err := st.readMeta()
	assert.NoError(t, err)

	assert.Equal(t, "1", meta.StreamNo)
	assert.Equal(t, int64(100), meta.MessageID)
	assert.Equal(t, "c1", meta.ChannelID)
	assert.Equal(t, uint8(1), meta.ChannelType)
	assert.Equal(t, uint32(2), meta.MessageSeq)
	assert.Equal(t, wkproto.StreamFlagEnd, meta.StreamFlag)

}
