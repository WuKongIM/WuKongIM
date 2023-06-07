package wkstore

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func makeSegment(t *testing.T, dir string) *segment {
	cfg := NewStoreConfig()
	cfg.SegmentMaxBytes = 1024 * 1024 * 10

	tp := newTopic("test", 0, cfg)
	sg := newSegment(tp, 0, cfg)
	sg.init(SegmentModeAll)
	return sg
}

func TestSegmentAppend(t *testing.T) {
	dir, err := ioutil.TempDir("", "commitlog-index")
	assert.NoError(t, err)
	sg := makeSegment(t, dir)
	defer func() {
		sg.close()
		os.Remove(dir)
	}()

	messages := make([]Message, 0)
	for i := 0; i < 100000; i++ {
		m := &testMessage{
			messageID: int64(i),
			data:      []byte("dsakjdaksdhakjdhalkhdljkahaljdhjkaj"),
			seq:       uint32(i + 1000),
		}
		messages = append(messages, m)

	}
	_, err = sg.appendMessages(messages)
	assert.NoError(t, err)

	// var messageSeq uint32 = 1230
	// resultMessage := &testMessage{}
	// lg, err := sg.ReadAt(int64(messageSeq))
	// resultMessage.Decode(lg.Data)
	// assert.NoError(t, err)
	// assert.Equal(t, messageSeq, resultMessage.MessageSeq)
}

func TestSanitySimpleCheck(t *testing.T) {
	dir, err := ioutil.TempDir("", "commitlog-index")
	assert.NoError(t, err)
	sg := makeSegment(t, dir)
	defer func() {
		sg.close()
		os.Remove(dir)
	}()
	fstat, err := sg.segmentFile.Stat()
	assert.NoError(t, err)
	ok, _, err := sg.sanitySimpleCheck(fstat.Size())
	assert.NoError(t, err)
	assert.Equal(t, true, ok)

	// lg, err := DecodeLogAt(sg.logFile, start)
	// assert.NoError(t, err)

	// fmt.Println("lg.Offset--->", lg.Offset)

}

func TestSegmentSanityCheck(t *testing.T) {
	dir, err := ioutil.TempDir("", "commitlog-index")
	assert.NoError(t, err)
	sg := makeSegment(t, dir)

	defer func() {
		sg.close()
		os.Remove(dir)
	}()
	messages := make([]Message, 0)
	for i := 0; i < 10000; i++ {
		m := &testMessage{
			messageID: int64(i),
			seq:       uint32(i + 1000),
			data:      []byte("dsakjdaksdhakjdhalkhdljkahaljdhjkaj"),
		}
		messages = append(messages, m)

	}
	_, err = sg.appendMessages(messages)
	assert.NoError(t, err)
	_, err = sg.sanityCheck()
	assert.NoError(t, err)

	sg.segmentFile.Truncate(int64(sg.position - 8))

	_, err = sg.sanityCheck()
	assert.NoError(t, err)

}

func TestSegmentReadLogs(t *testing.T) {
	dir, err := ioutil.TempDir("", "commitlog-index")
	assert.NoError(t, err)
	cfg := NewStoreConfig()
	cfg.SegmentMaxBytes = 1024 * 1024 * 10
	tp := newTopic("test", 0, cfg)
	sg := newSegment(tp, 0, NewStoreConfig())

	defer func() {
		sg.close()
		os.Remove(dir)
	}()
	messages := make([]Message, 0)
	for i := 0; i < 100000; i++ {
		m := &testMessage{
			messageID: int64(i),
			seq:       uint32(i + 1000),
		}
		messages = append(messages, m)

	}
	_, err = sg.appendMessages(messages)
	assert.NoError(t, err)
	// messages := make([]Message, 0)
	// err = sg.ReadLogs(2001, 40, func(lg *Log) error {
	// 	m := &Message{}
	// 	err = m.Decode(lg.Data)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	messages = append(messages, m)
	// 	return nil
	// })
	// assert.NoError(t, err)

	// assert.Equal(t, 40, len(messages))

}
