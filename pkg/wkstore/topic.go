package wkstore

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type topic struct {
	f                  *FileStore
	name               string
	slot               uint32
	segments           []uint32
	lastSegment        *segment
	lastBaseMessageSeq uint32 // last segment messageSeq
	topicDir           string
	wklog.Log
	appendLock sync.RWMutex
	lastMsgSeq atomic.Uint32
}

func newTopic(name string, slot uint32, f *FileStore) *topic {
	topicDir := filepath.Join(f.cfg.DataDir, fmt.Sprintf("%d", slot), "topics", name)

	t := &topic{
		name:     name,
		slot:     slot,
		f:        f,
		topicDir: topicDir,
		Log:      wklog.NewWKLog(fmt.Sprintf("%d-topic[%s]", slot, name)),
		segments: make([]uint32, 0),
	}
	err := os.MkdirAll(t.topicDir, FileDefaultMode)
	if err != nil {
		t.Error("mkdir slot dir fail")
		panic(err)
	}
	t.initSegments()

	return t
}

func (t *topic) appendMessages(msgs []Message) ([]uint32, error) {
	t.appendLock.Lock()
	defer t.appendLock.Unlock()

	if t.lastSegment == nil {
		t.resetLastSegment()
	}

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
		return nil, nil
	}
	t.lastMsgSeq.Store(lastMsg.GetSeq())

	//	if  roll new segment
	if t.lastSegment.index.IsFull() || int64(t.lastSegment.position) > t.f.cfg.SegmentMaxBytes {
		t.roll(msgs[len(msgs)-1]) // roll new segment
	}

	// append message to segment
	err := t.lastSegment.appendMessages(msgs)
	if err != nil {
		return nil, err
	}
	return seqs, nil
}

func (t *topic) roll(m Message) {
	t.lastBaseMessageSeq = m.GetSeq()
	t.segments = append(t.segments, m.GetSeq())

	t.resetLastSegment()
}

func (t *topic) getSegment(baseOffset uint32, mode SegmentMode) *segment {
	key := t.getSegmentCacheKey(baseOffset)
	seg, _ := t.f.segmentCache.Get(key)
	if seg != nil {
		return seg
	}
	seg = newSegment(baseOffset, t)
	err := seg.init(mode)
	if err != nil {
		panic(err)
	}
	return seg
}

func (t *topic) initSegments() {
	t.segments = t.sortSegmentBaseOffsets(t.getAllSegmentBaseOffset())
	if len(t.segments) == 0 {
		t.segments = append(t.segments, 0)
	}
	t.lastBaseMessageSeq = t.segments[len(t.segments)-1]

	t.resetLastSegment()
}

func (t *topic) resetLastSegment() {
	t.lastSegment = t.getSegment(t.lastBaseMessageSeq, SegmentModeAll)
	t.lastMsgSeq.Store(t.lastSegment.lastMsgSeq.Load())
}

func (t *topic) getSegmentCacheKey(baseOffset uint32) string {
	return fmt.Sprintf("%s-%d", t.name, baseOffset)
}

// get all segment base offset
func (t *topic) getAllSegmentBaseOffset() []uint32 {
	files, err := ioutil.ReadDir(t.topicDir)
	if err != nil {
		t.Error("read dir fail!", zap.String("topicDir", t.topicDir))
		panic(err)
	}
	segments := make([]uint32, 0)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), segmentSuffix) {
			offsetStr := strings.TrimSuffix(f.Name(), segmentSuffix)
			baseOffset, err := strconv.ParseInt(offsetStr, 10, 64)
			if err != nil {
				continue
			}
			segments = append(segments, uint32(baseOffset))
		}
	}
	return segments

}
func (t *topic) sortSegmentBaseOffsets(offsets []uint32) []uint32 {
	for n := 0; n <= len(offsets); n++ {
		for i := 1; i < len(offsets)-n; i++ {
			if offsets[i] < offsets[i-1] {
				offsets[i], offsets[i-1] = offsets[i-1], offsets[i]
			}
		}
	}
	return offsets
}

func (t *topic) nextMsgSeq() uint32 {
	return t.lastMsgSeq.Inc()
}

func (t *topic) getLastMsgSeq() uint32 {
	return t.lastMsgSeq.Load()
}

func (t *topic) close() {
	if t.lastSegment != nil {
		t.lastSegment.close()
	}
}

type topicState struct {
}
