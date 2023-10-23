package multiraft

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/multiraft/wal"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

// type SateStorage interface {
// 	InitialState() (hardState pb.HardState, confState pb.ConfState, err error)
// 	SetHardState(st pb.HardState) error
// 	HardState() (pb.HardState, error)
// 	SetConfState(confState pb.ConfState) error
// 	SetApplied(applied uint64) error
// 	Applied() (uint64, error)
// }

// type LogStorage struct {
// 	walStore    *WALStorage
// 	replicaID   uint32
// 	raftStorage ReplicaRaftStorage
// 	peers       []Peer
// }

// func NewLogStorage(replicaID uint32, walStore *WALStorage, raftStorage ReplicaRaftStorage, peers []Peer) *LogStorage {
// 	return &LogStorage{
// 		peers:       peers,
// 		replicaID:   replicaID,
// 		walStore:    walStore,
// 		raftStorage: raftStorage,
// 	}
// }

// func (l *LogStorage) InitialState() (hardState pb.HardState, confState pb.ConfState, err error) {

// 	confState, err = l.raftStorage.GetConfState(l.replicaID)
// 	if err != nil {
// 		return
// 	}
// 	if len(confState.Voters) == 0 {
// 		peerIDs := make([]uint64, 0, len(l.peers))
// 		for _, peer := range l.peers {
// 			peerIDs = append(peerIDs, peer.ID)
// 		}
// 		confState.Voters = peerIDs
// 	}
// 	hardState, err = l.raftStorage.GetHardState(l.replicaID)
// 	if err != nil {
// 		return
// 	}
// 	return
// }

// func (l *LogStorage) SetHardState(st pb.HardState) error {
// 	return l.raftStorage.SetHardState(l.replicaID, st)
// }

// func (l *LogStorage) HardState() (pb.HardState, error) {
// 	return l.raftStorage.GetHardState(l.replicaID)
// }

// func (l *LogStorage) SetConfState(confState pb.ConfState) error {
// 	return l.raftStorage.SetConfState(l.replicaID, confState)
// }

// func (l *LogStorage) SetApplied(applied uint64) error {
// 	return l.raftStorage.SetApplied(l.replicaID, applied)
// }

// func (l *LogStorage) Applied() (uint64, error) {
// 	return l.raftStorage.GetApplied(l.replicaID)
// }

// func (l *LogStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
// 	return l.walStore.Entries(lo, hi, maxSize)
// }

// func (l *LogStorage) Append(entries []pb.Entry) error {
// 	return l.walStore.Append(entries)
// }

// func (l *LogStorage) Term(i uint64) (uint64, error) {
// 	return l.walStore.Term(i)
// }

// func (l *LogStorage) LastIndex() (uint64, error) {
// 	return l.walStore.LastIndex()
// }

// func (l *LogStorage) FirstIndex() (uint64, error) {
// 	return l.walStore.FirstIndex()
// }

// func (l *LogStorage) Snapshot() (pb.Snapshot, error) {
// 	return l.walStore.Snapshot()
// }

// func (l *LogStorage) ApplySnapshot(snap pb.Snapshot) error {
// 	return l.walStore.ApplySnapshot(snap)
// }

type WALStorage struct {
	sync.Mutex
	walLog *wal.Log
	wklog.Log

	hardStateKey      []byte
	confStateKey      []byte
	committedIndexKey []byte
	appliedKey        []byte
	walPath           string
	metaPath          string
}

func NewWALStorage(walPath string) *WALStorage {
	w := &WALStorage{
		Log:               wklog.NewWKLog("WALStorage"),
		hardStateKey:      []byte("hardState"),
		committedIndexKey: []byte("committedIndex"),
		confStateKey:      []byte("confState"),
		appliedKey:        []byte("appliedKey"),
		walPath:           walPath,
	}

	return w
}

func (w *WALStorage) Exist() bool {
	_, err := os.Stat(w.metaPath)
	return err == nil
}

func (w *WALStorage) Open() error {
	lg, err := wal.Open(w.walPath, wal.DefaultOptions)
	if err != nil {
		return err
	}
	w.walLog = lg

	return nil
}

func (w *WALStorage) Close() error {
	err := w.walLog.Close()
	if err != nil {
		w.Warn("close wal log error", zap.Error(err))
	}

	return nil
}

func (w *WALStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	fmt.Println("Entries------->", lo, hi, maxSize)

	entries := make([]pb.Entry, 0, hi)
	for i := lo; i <= hi-1; i++ {
		ent, err := w.readEntry(i)
		if err != nil {
			if errors.Is(err, wal.ErrNotFound) {
				break
			}
			return nil, err
		}
		entries = append(entries, ent)
	}
	return limitSize(entries, maxSize), nil
}

func (w *WALStorage) Append(entries []pb.Entry) error {
	fmt.Println("Append------->", entries)
	if len(entries) == 0 {
		return nil
	}

	lastIdx, _ := w.LastIndex()

	if lastIdx >= entries[len(entries)-1].Index { //
		return nil
	}

	if lastIdx >= entries[0].Index {
		entries = entries[lastIdx-entries[0].Index+1:]
	}
	if len(entries) == 0 {
		return nil
	}

	if len(entries) > 1 {
		batch := new(wal.Batch)
		for _, ent := range entries {
			entData, err := ent.Marshal()
			if err != nil {
				return err
			}
			batch.Write(ent.Index, entData)
		}
		return w.walLog.WriteBatch(batch)
	} else {
		ent := entries[0]
		entData, err := ent.Marshal()
		if err != nil {
			return err
		}
		return w.walLog.Write(ent.Index, entData)
	}
}

func (w *WALStorage) Term(i uint64) (uint64, error) {

	entry, err := w.readEntry(i)
	if err != nil {
		if errors.Is(err, wal.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return entry.Term, nil
}

func (w *WALStorage) LastIndex() (uint64, error) {
	return w.walLog.LastIndex()
}

func (w *WALStorage) FirstIndex() (uint64, error) {

	// firstIndex, err := w.walLog.FirstIndex()
	// if err != nil {
	// 	return 0, err
	// }
	// return firstIndex + 1, nil
	return 1, nil // TODO: 因为没有快照，所以这里应该永远返回1
}

func (w *WALStorage) Snapshot() (pb.Snapshot, error) {

	return pb.Snapshot{}, nil
}
func (w *WALStorage) ApplySnapshot(snap pb.Snapshot) error {
	panic("no implement Snapshot")
}

func (w *WALStorage) readEntry(index uint64) (pb.Entry, error) {
	data, err := w.walLog.Read(index)
	if err != nil {
		return pb.Entry{}, err
	}
	var ent pb.Entry
	err = ent.Unmarshal(data)
	return ent, err
}

func limitSize(ents []pb.Entry, maxSize uint64) []pb.Entry {
	if len(ents) == 0 {
		return ents
	}
	size := ents[0].Size()
	var limit int
	for limit = 1; limit < len(ents); limit++ {
		size += ents[limit].Size()
		if uint64(size) > maxSize {
			break
		}
	}
	return ents[:limit]
}

type RaftStorage interface {
	raft.Storage
	Append(entries []pb.Entry) error
	SetHardState(hardState pb.HardState) error
	SetConfState(confState pb.ConfState) error
}

type MemoryRaftStorage struct {
	memoryStorage *raft.MemoryStorage
	confsate      *pb.ConfState
}

func NewMemoryRaftStorage() *MemoryRaftStorage {
	return &MemoryRaftStorage{
		memoryStorage: raft.NewMemoryStorage(),
	}
}

func (m *MemoryRaftStorage) InitialState() (pb.HardState, pb.ConfState, error) {
	return m.memoryStorage.InitialState()
}

func (m *MemoryRaftStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	return m.memoryStorage.Entries(lo, hi, maxSize)
}

func (m *MemoryRaftStorage) Term(i uint64) (uint64, error) {
	return m.memoryStorage.Term(i)
}

func (m *MemoryRaftStorage) LastIndex() (uint64, error) {
	return m.memoryStorage.LastIndex()
}

func (m *MemoryRaftStorage) FirstIndex() (uint64, error) {
	return m.memoryStorage.FirstIndex()
}

func (m *MemoryRaftStorage) Snapshot() (pb.Snapshot, error) {
	return m.memoryStorage.Snapshot()
}

func (m *MemoryRaftStorage) ApplySnapshot(snap pb.Snapshot) error {
	return m.memoryStorage.ApplySnapshot(snap)
}

func (m *MemoryRaftStorage) SetHardState(hardState pb.HardState) error {
	return m.memoryStorage.SetHardState(hardState)
}

func (m *MemoryRaftStorage) SetConfState(confState pb.ConfState) error {
	m.confsate = &confState
	return nil
}
func (m *MemoryRaftStorage) GetConfState() pb.ConfState {
	return *m.confsate
}

func (m *MemoryRaftStorage) Append(entries []pb.Entry) error {
	fmt.Println("Append...........", entries[len(entries)-1].Index)
	return m.memoryStorage.Append(entries)
}
