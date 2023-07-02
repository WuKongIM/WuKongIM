package wkstore

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
)

type Stream struct {
	cfg      *StoreConfig
	topicDir string
	streamNo string
	streamIO *os.File

	streamMetaIO       *os.File
	maxStreamSeqLoaded bool
	maxStreamSeq       uint32
	metaLock           sync.RWMutex

	sync.RWMutex
}

func NewStream(streamNo string, topicDir string, cfg *StoreConfig) *Stream {

	m := &Stream{
		streamNo:           streamNo,
		topicDir:           topicDir,
		cfg:                cfg,
		maxStreamSeqLoaded: false,
	}
	return m
}

func (m *Stream) appendItem(item *StreamItem) (uint32, error) {
	m.Lock()
	defer m.Unlock()

	nextStreamSeq, err := m.nextStreamSeqNoLock()
	if err != nil {
		return 0, err
	}
	item.StreamSeq = nextStreamSeq
	_, err = m.getStreamIO().Write(EncodeStreamItem(item))
	if err != nil {
		return 0, err
	}
	m.maxStreamSeq = nextStreamSeq
	return nextStreamSeq, nil
}

func (m *Stream) readItems() ([]*StreamItem, error) {
	m.Lock()
	defer m.Unlock()

	var startOffset int64 = 0
	var items = StreamItemSlice{}
	stat, err := m.getStreamIO().Stat()
	if err != nil {
		return nil, err
	}
	fileSize := stat.Size()
	if fileSize == 0 {
		return nil, nil
	}
	for {
		item, len, err := currentStreamItem(m.getStreamIO(), startOffset, true)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		startOffset += int64(len)
		if startOffset >= fileSize {
			break
		}
	}
	sort.Sort(items)
	return items, nil

}

func (m *Stream) nextStreamSeqNoLock() (uint32, error) {
	if m.maxStreamSeqLoaded {
		return m.maxStreamSeq + 1, nil
	}
	var err error
	m.maxStreamSeq, err = m.readMaxStreamSeqNoLock()
	if err != nil {
		return 0, err
	}
	m.maxStreamSeqLoaded = true
	return m.maxStreamSeq + 1, err
}

func (m *Stream) readMaxStreamSeqNoLock() (uint32, error) {
	var startOffset int64 = 0
	var maxStreamSeq uint32 = 0
	stat, err := m.getStreamIO().Stat()
	if err != nil {
		return 0, err
	}
	fileSize := stat.Size()
	if fileSize == 0 {
		return 0, nil
	}
	for {
		item, len, err := currentStreamBaseInfo(m.getStreamIO(), startOffset)
		if err != nil {
			return 0, err
		}
		maxStreamSeq = item.StreamSeq
		startOffset += int64(len)
		if startOffset >= fileSize {
			break
		}
	}
	return maxStreamSeq, nil
}

func (m *Stream) saveMeta(meta *StreamMeta) error {
	m.metaLock.Lock()
	defer m.metaLock.Unlock()
	data := meta.Encode()

	metaIO := m.getMetaIO()
	metaIO.Seek(0, io.SeekStart)
	_, err := metaIO.Write(data)
	return err
}

func (m *Stream) readMeta() (*StreamMeta, error) {
	m.metaLock.Lock()
	defer m.metaLock.Unlock()
	metaIO := m.getMetaIO()
	metaIO.Seek(0, io.SeekStart)
	metaBytes, err := ioutil.ReadAll(metaIO)
	if err != nil {
		return nil, err
	}
	if len(metaBytes) == 0 {
		return nil, nil
	}
	meta := &StreamMeta{}
	err = meta.Decode(metaBytes)
	return meta, err
}

func (m *Stream) streamEnd() error {
	meta, err := m.readMeta()
	if err != nil {
		return err
	}
	if meta == nil {
		return nil
	}
	meta.StreamFlag = wkproto.StreamFlagEnd

	return m.saveMeta(meta)
}

func (m *Stream) getMetaIO() *os.File {
	if m.streamMetaIO == nil {
		var err error
		m.streamMetaIO, err = os.OpenFile(m.streamMetaPath(), os.O_RDWR|os.O_CREATE|os.O_TRUNC, FileDefaultMode)
		if err != nil {
			panic(err)
		}
	}
	return m.streamMetaIO
}

func (m *Stream) getStreamIO() *os.File {
	if m.streamIO == nil {
		var err error
		m.streamIO, err = os.OpenFile(m.streamPath(), os.O_RDWR|os.O_CREATE|os.O_APPEND, FileDefaultMode)
		if err != nil {
			panic(err)
		}
	}
	return m.streamIO
}

func (m *Stream) streamMetaPath() string {
	return filepath.Join(m.topicDir, fmt.Sprintf("%s%s", m.streamNo, streamMetaSuffix))
}
func (m *Stream) streamPath() string {
	return filepath.Join(m.topicDir, fmt.Sprintf("%s%s", m.streamNo, streamSuffix))
}

func (m *Stream) close() {
	m.metaLock.Lock()
	defer m.metaLock.Unlock()
	if m.streamIO != nil {
		m.streamIO.Close()
	}

	m.Lock()
	defer m.Unlock()

	if m.streamMetaIO != nil {
		m.streamMetaIO.Close()
	}
}
