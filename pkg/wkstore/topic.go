package wkstore

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type topic struct {
	cfg                *StoreConfig
	name               string
	slot               uint32
	segments           []uint32
	lastBaseMessageSeq uint32 // last segment messageSeq
	topicDir           string
	wklog.Log
	appendLock sync.RWMutex
	lastMsgSeq atomic.Uint32

	getSegmentLock sync.RWMutex

	streamCache *lru.Cache[string, *Stream]
}

func newTopic(name string, slot uint32, cfg *StoreConfig) *topic {

	topicDir := filepath.Join(cfg.DataDir, fmt.Sprintf("%d", slot), "topics", name)

	cache, err := lru.NewWithEvict(cfg.StreamCacheSize, func(key string, stream *Stream) {
		stream.close()
	})
	if err != nil {
		panic(err)
	}

	t := &topic{
		name:        name,
		slot:        slot,
		cfg:         cfg,
		topicDir:    topicDir,
		Log:         wklog.NewWKLog(fmt.Sprintf("%d-topic[%s]", slot, name)),
		segments:    make([]uint32, 0),
		streamCache: cache,
	}
	err = os.MkdirAll(t.topicDir, FileDefaultMode)
	if err != nil {
		t.Error("mkdir slot dir fail")
		panic(err)
	}
	t.initSegments()

	return t
}

func (t *topic) appendMessages(msgs []Message) ([]uint32, int, error) {
	t.appendLock.Lock()
	defer t.appendLock.Unlock()

	lastSegment := t.getActiveSegment()

	preLastMsgSeq := t.lastMsgSeq.Load()

	// fill messageSeq
	seqs := make([]uint32, 0, len(msgs))
	for _, msg := range msgs { // set msg seq
		seq := t.nextMsgSeq()
		msg.SetSeq(seq)
		seqs = append(seqs, seq)
	}

	// update lastMsgSeq
	lastMsg := msgs[len(msgs)-1]
	if preLastMsgSeq >= lastMsg.GetSeq() {
		t.Warn("message is exist, not save", zap.Uint32("msgSeq", lastMsg.GetSeq()), zap.Uint32("lastMsgSeq", t.lastMsgSeq.Load()))
		return nil, 0, nil
	}
	t.lastMsgSeq.Store(lastMsg.GetSeq())

	//	if  roll new segment
	if lastSegment.index.IsFull() || int64(lastSegment.position) > t.cfg.SegmentMaxBytes {
		t.roll(msgs[len(msgs)-1]) // roll new segment
	}

	// append message to segment
	n, err := lastSegment.appendMessages(msgs)
	if err != nil {
		return nil, 0, err
	}
	return seqs, n, nil
}

func (t *topic) saveStreamMeta(meta *StreamMeta) error {
	stream, err := t.getStream(meta.StreamNo)
	if err != nil {
		return err
	}
	return stream.saveMeta(meta)
}

func (t *topic) readStreamMeta(streamNo string) (*StreamMeta, error) {
	stream, err := t.getStream(streamNo)
	if err != nil {
		return nil, err
	}
	return stream.readMeta()
}

func (t *topic) appendStreamItem(streamNo string, item *StreamItem) (uint32, error) {

	stream, err := t.getStream(streamNo)
	if err != nil {
		return 0, err
	}
	streamSeq, err := stream.appendItem(item)
	if err != nil {
		return 0, err
	}

	return streamSeq, nil
}

func (t *topic) readItems(streamNo string) ([]*StreamItem, error) {
	stream, err := t.getStream(streamNo)
	if err != nil {
		return nil, err
	}
	return stream.readItems()
}

func (t *topic) streamEnd(streamNo string) error {
	stream, err := t.getStream(streamNo)
	if err != nil {
		return err
	}
	err = stream.streamEnd()
	if err != nil {
		return err
	}
	t.streamCache.Remove(streamNo) // 移除会触发stream close
	return nil
}

func (t *topic) getStream(streamNo string) (*Stream, error) {
	if stream, ok := t.streamCache.Get(streamNo); ok {
		return stream, nil
	}
	stream := NewStream(streamNo, t.topicDir, t.cfg)
	t.streamCache.Add(streamNo, stream)
	return stream, nil
}

func (t *topic) appendMessage(msg Message) (uint32, int, error) {
	messageSeqs, n, err := t.appendMessages([]Message{msg})
	if err != nil {
		return 0, 0, err
	}
	return messageSeqs[0], n, err
}

func (t *topic) readLastMessages(limit uint64, callback func(msg Message) error) error {
	actLimit := limit
	var actMessageSeq uint32 = 0
	lastSeq := uint64(t.lastMsgSeq.Load())
	if lastSeq < limit {
		actLimit = uint64(t.lastMsgSeq.Load()) + 1 // +1表示包含最后一条lastMsgSeq的消息
		actMessageSeq = 0
	} else {
		actLimit = limit
		actMessageSeq = t.lastMsgSeq.Load() - uint32(limit) + 1 // +1表示包含最后一条lastMsgSeq的消息
	}
	return t.readMessages(actMessageSeq, actLimit, callback)
}

// ReadLogs ReadLogs
func (t *topic) readMessages(messageSeq uint32, limit uint64, callback func(msg Message) error) error {
	baseMessageSeq, err := t.calcBaseMessageSeq(messageSeq)
	if err != nil {
		return err
	}
	readCount := 0
	var segment = t.getSegment(baseMessageSeq, SegmentModeAll) // 获取baseOffset的segment
	err = segment.readMessages(messageSeq, limit, func(m Message) error {
		readCount++
		if callback != nil {
			err := callback(m)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	nextBaseMessageSeq := baseMessageSeq
	for readCount < int(limit) {
		nextBaseMessageSeqInt64 := t.nextBaseMessageSeq(nextBaseMessageSeq)
		nextBaseMessageSeq = uint32(nextBaseMessageSeqInt64)
		if nextBaseMessageSeqInt64 != -1 {
			nextSegment := t.getSegment(nextBaseMessageSeq, SegmentModeAll)
			err := nextSegment.readMessagesAtPosition(0, limit-uint64(readCount), func(m Message) error {
				readCount++
				if callback != nil {
					err := callback(m)
					if err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Error("Failed to read the remaining logs", zap.Error(err), zap.Int64("position", 0), zap.Uint64("limit", limit-uint64(readCount)))
				return err
			}
		} else {
			break
		}
	}
	return nil
}

// readMessageAt readMessageAt
func (t *topic) readMessageAt(messageSeq uint32) (Message, error) {
	baseMessageSeq, err := t.calcBaseMessageSeq(messageSeq)
	if err != nil {
		return nil, err
	}
	var segment = t.getSegment(baseMessageSeq, SegmentModeAll) // 获取baseOffset的segment
	return segment.readAt(messageSeq)
}

func (t *topic) roll(m Message) {
	lastSegment := t.getActiveSegment()
	if lastSegment != nil {
		segmentCache.Remove(t.getSegmentCacheKey(t.lastBaseMessageSeq))
	}
	t.lastBaseMessageSeq = m.GetSeq()
	t.segments = append(t.segments, m.GetSeq())

	t.resetActiveSegment(t.lastBaseMessageSeq)
}

func (t *topic) nextBaseMessageSeq(baseMessageSeq uint32) int64 {
	for i := 0; i < len(t.segments); i++ {
		baseOffst := t.segments[i]
		if baseOffst == baseMessageSeq {
			if i+1 < len(t.segments) {
				return int64(t.segments[i+1])
			}
			break
		}
	}
	return -1
}

func (t *topic) calcBaseMessageSeq(messageSeq uint32) (uint32, error) {
	for i := 0; i < len(t.segments); i++ {
		baseMessageSeq := t.segments[i]
		if i+1 < len(t.segments) {
			if messageSeq >= baseMessageSeq && messageSeq <= t.segments[i+1] {
				return baseMessageSeq, nil
			}
		} else {
			if messageSeq >= baseMessageSeq {
				return baseMessageSeq, nil
			}
		}
	}
	return 0, errors.New("baseOffset not found")
}

func (t *topic) getSegment(baseMessageSeq uint32, mode SegmentMode) *segment {

	t.getSegmentLock.Lock()
	defer t.getSegmentLock.Unlock()
	key := t.getSegmentCacheKey(baseMessageSeq)
	seg, _ := segmentCache.Get(key)
	if seg != nil {
		return seg
	}
	seg = newSegment(t, baseMessageSeq, t.cfg)
	err := seg.init(mode)
	if err != nil {
		panic(err)
	}
	segmentCache.Add(key, seg)
	return seg
}

// 当前可以写的segment
func (t *topic) getActiveSegment() *segment {
	lastBaseOffset := t.segments[len(t.segments)-1]
	return t.getSegment(lastBaseOffset, SegmentModeAll)
}

func (t *topic) initSegments() {
	t.segments = t.sortSegmentBaseMessageSeqs(t.getAllSegmentBaseMessageSeq())
	if len(t.segments) == 0 {
		t.segments = append(t.segments, 0)
	}
	t.lastBaseMessageSeq = t.segments[len(t.segments)-1]

	t.resetActiveSegment(t.lastBaseMessageSeq)
}

// 重新设置激活的segment
func (t *topic) resetActiveSegment(baseMessageSeq uint32) *segment {
	seg := t.getSegment(baseMessageSeq, SegmentModeAll)
	t.lastMsgSeq.Store(seg.lastMsgSeq.Load())
	return seg
}

func (t *topic) getSegmentCacheKey(baseMessageSeq uint32) string {
	return fmt.Sprintf("%s-%d", t.name, baseMessageSeq)
}

// get all segment base messageSeq
func (t *topic) getAllSegmentBaseMessageSeq() []uint32 {
	files, err := ioutil.ReadDir(t.topicDir)
	if err != nil {
		t.Error("read dir fail!", zap.String("topicDir", t.topicDir))
		panic(err)
	}
	segments := make([]uint32, 0)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), segmentSuffix) {
			baseMessageSeqStr := strings.TrimSuffix(f.Name(), segmentSuffix)
			baseMessageSeq, err := strconv.ParseInt(baseMessageSeqStr, 10, 64)
			if err != nil {
				continue
			}
			segments = append(segments, uint32(baseMessageSeq))
		}
	}
	return segments

}
func (t *topic) sortSegmentBaseMessageSeqs(baseMessageSeqs []uint32) []uint32 {
	for n := 0; n <= len(baseMessageSeqs); n++ {
		for i := 1; i < len(baseMessageSeqs)-n; i++ {
			if baseMessageSeqs[i] < baseMessageSeqs[i-1] {
				baseMessageSeqs[i], baseMessageSeqs[i-1] = baseMessageSeqs[i-1], baseMessageSeqs[i]
			}
		}
	}
	return baseMessageSeqs
}

func (t *topic) nextMsgSeq() uint32 {
	return t.lastMsgSeq.Inc()
}

func (t *topic) getLastMsgSeq() uint32 {
	return t.lastMsgSeq.Load()
}

func (t *topic) close() {

}
