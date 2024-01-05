package cluster

import (
	"encoding/binary"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/key"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

type PebbleStorage struct {
	db   *pebble.DB
	path string
	wklog.Log
	wo *pebble.WriteOptions
}

func NewPebbleStorage(path string) *PebbleStorage {
	return &PebbleStorage{
		path: path,
		Log:  wklog.NewWKLog("pebbleStorage"),
		wo: &pebble.WriteOptions{
			Sync: true,
		},
	}
}

func (p *PebbleStorage) Open() error {
	var err error
	p.db, err = pebble.Open(p.path, &pebble.Options{})
	if err != nil {
		return err
	}
	return nil
}

func (p *PebbleStorage) Close() {
	err := p.db.Close()
	if err != nil {
		p.Warn("close pebble db err", zap.Error(err))
	}
}

func (p *PebbleStorage) AppendLog(shardNo string, log replica.Log) error {

	logData, err := log.Marshal()
	if err != nil {
		p.Error("marshal log err", zap.Error(err))
		return err
	}

	keyData := key.NewLogKey(shardNo, log.Index)
	err = p.db.Set(keyData, logData, p.wo)
	if err != nil {
		p.Error("append log err", zap.Error(err))
		return err
	}
	return p.saveMaxIndex(shardNo, log.Index)
}

func (p *PebbleStorage) saveMaxIndex(shardNo string, index uint64) error {
	maxIndexKeyData := key.NewMaxIndexKey(shardNo)
	maxIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(maxIndexdata, index)
	err := p.db.Set(maxIndexKeyData, maxIndexdata, p.wo)
	return err
}
func (p *PebbleStorage) GetLogs(shardNo string, startLogIndex uint64, limit uint32) ([]replica.Log, error) {

	lastIndex, err := p.LastIndex(shardNo)
	if err != nil {
		return nil, err
	}
	if lastIndex == 0 {
		return nil, nil
	}
	if startLogIndex > lastIndex {
		return nil, nil
	}

	iter := p.db.NewIter(&pebble.IterOptions{
		LowerBound: key.NewLogKey(shardNo, startLogIndex),
		UpperBound: key.NewLogKey(shardNo, math.MaxUint64),
	})
	defer iter.Close()
	logs := make([]replica.Log, 0, limit)
	for iter.First(); iter.Valid(); iter.Next() {
		log := &replica.Log{}
		err := log.Unmarshal(iter.Value())
		if err != nil {
			p.Panic("unmarshal log err", zap.Error(err))
		}
		logs = append(logs, *log)
		if len(logs) >= int(limit) {
			break
		}
	}
	return logs, nil
}

func (p *PebbleStorage) LastIndex(shardNo string) (uint64, error) {
	maxIndexKeyData := key.NewMaxIndexKey(shardNo)
	maxIndexdata, closer, err := p.db.Get(maxIndexKeyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	if len(maxIndexdata) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(maxIndexdata), nil
}

func (p *PebbleStorage) FirstIndex(shardNo string) (uint64, error) {
	return 0, nil
}

func (p *PebbleStorage) SetAppliedIndex(shardNo string, index uint64) error {
	appliedIndexKeyData := key.NewAppliedIndexKey(shardNo)
	appliedIndexdata := make([]byte, 8)
	binary.BigEndian.PutUint64(appliedIndexdata, index)
	err := p.db.Set(appliedIndexKeyData, appliedIndexdata, p.wo)
	return err
}

func (p *PebbleStorage) GetAppliedIndex(shardNo string) (uint64, error) {
	appliedIndexKeyData := key.NewAppliedIndexKey(shardNo)
	appliedIndexdata, closer, err := p.db.Get(appliedIndexKeyData)
	if err != nil {
		if err == pebble.ErrNotFound {
			return 0, nil
		}
		return 0, err
	}
	defer closer.Close()
	if len(appliedIndexdata) == 0 {
		return 0, nil
	}
	return binary.BigEndian.Uint64(appliedIndexdata), nil
}
