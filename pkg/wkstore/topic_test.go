package wkstore

import (
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestTopic() *topic {
	cfg := newTestStoreConfig()
	cfg.SegmentMaxBytes = 1024 * 1024
	cfg.DecodeMessageFnc = func(msg []byte) (Message, error) {
		tm := &testMessage{}
		err := tm.Decode(msg)
		return tm, err
	}
	tc := newTopic("test", 100, cfg)
	return tc
}

func TestTopicAppend(t *testing.T) {
	tc := newTestTopic()
	defer func() {
		tc.close()
		os.Remove(tc.cfg.DataDir)
	}()
	var totalBytes int64 = 0
	for i := 0; i < 100000; i++ {
		m := &testMessage{
			messageID: int64(i),
			seq:       uint32(i),
			data:      []byte("this is test"),
		}
		_, n, err := tc.appendMessage(m)
		assert.NoError(t, err)
		totalBytes += int64(n)
	}

	expectFileNum := totalBytes / tc.cfg.SegmentMaxBytes
	if totalBytes%tc.cfg.SegmentMaxBytes != 0 {
		expectFileNum++
	}

	assert.Equal(t, int(expectFileNum), len(tc.segments))
}

func BenchmarkTopicAppendParallel(b *testing.B) {

	tc := newTestTopic()
	tc.cfg.SegmentMaxBytes = 1024 * 1024 * 1024

	defer func() {
		tc.close()
		os.Remove(tc.cfg.DataDir)
	}()

	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			m := &testMessage{
				data: []byte(fmt.Sprintf("this is test -> %d", rand.Intn(100000))),
			}
			_, _, err := tc.appendMessage(m)
			assert.NoError(b, err)
		}
	})
}

func TestReadLastLogs(t *testing.T) {
	//dir, err := ioutil.TempDir("", "commitlog-index")
	//assert.NoError(t, err)

	var maxBytesPerLogFile int64 = 1024 * 1024 * 1024
	tc := newTestTopic()
	tc.cfg.SegmentMaxBytes = maxBytesPerLogFile

	defer func() {
		tc.close()
		os.Remove(tc.cfg.DataDir)
	}()
	var totalBytes int64 = 0
	var totalnum = 10000
	for i := 0; i < totalnum; i++ {
		m := &testMessage{
			messageID: int64(i),
			data:      []byte("this is test"),
		}
		_, n, err := tc.appendMessage(m)
		assert.NoError(t, err)
		totalBytes += int64(n)
	}
	var messages = make([]Message, 0)
	readNum := 400
	err := tc.readLastMessages(uint64(readNum), func(m Message) error {
		messages = append(messages, m)
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, readNum, len(messages))
	assert.Equal(t, totalnum, int(messages[readNum-1].GetSeq()))

}

// func TestReadLastLogs2(t *testing.T) {
// 	dir, err := ioutil.TempDir("", "commitlog-index")
// 	assert.NoError(t, err)

// 	var maxBytesPerLogFile int64 = 1024
// 	tc := NewTopic(dir, "test", maxBytesPerLogFile)

// 	defer func() {
// 		tc.Close()
// 		os.Remove(dir)
// 	}()

// 	for i := 0; i < 41; i++ {
// 		seq, _ := tc.NextOffset()
// 		m := &Message{
// 			MessageID:   int64(i),
// 			MessageSeq:  seq,
// 			ChannelID:   fmt.Sprintf("test%d", i),
// 			ChannelType: 2,
// 			FromUID:     fmt.Sprintf("test-%d", i),
// 			Payload:     []byte(fmt.Sprintf("%d", i+1)),
// 		}
// 		_, err := tc.AppendLog(m)
// 		assert.NoError(t, err)
// 	}
// 	var messages = make([]*Message, 0)
// 	err = tc.ReadLastLogs(20, func(data []byte) error {
// 		m := &Message{}
// 		err := UnmarshalMessage(data, m)
// 		if err != nil {
// 			return err
// 		}
// 		messages = append(messages, m)
// 		return nil
// 	})
// 	assert.NoError(t, err)
// 	assert.Equal(t, 20, len(messages))
// }
