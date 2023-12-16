package cluster

import (
	"io"

	sm "github.com/lni/dragonboat/v4/statemachine"
)

type stateMachine struct {
	ShardID uint64
	PeerID  uint64
	opts    *RaftOptions
}

func newStateMachine(shardID uint64, peerID uint64, opts *RaftOptions) sm.IOnDiskStateMachine {
	return &stateMachine{
		ShardID: shardID,
		PeerID:  peerID,
		opts:    opts,
	}
}

func (s *stateMachine) Open(stopc <-chan struct{}) (uint64, error) {
	return 0, nil
}

func (s *stateMachine) Update(entries []sm.Entry) ([]sm.Entry, error) {
	return nil, nil
}

func (s *stateMachine) Sync() error {
	return nil
}

func (s *stateMachine) Lookup(query interface{}) (interface{}, error) {
	panic("implement me")
}

func (s *stateMachine) PrepareSnapshot() (interface{}, error) {
	panic("implement me")
}

func (s *stateMachine) SaveSnapshot(interface{}, io.Writer, <-chan struct{}) error {
	panic("implement me")
}

func (s *stateMachine) RecoverFromSnapshot(io.Reader, <-chan struct{}) error {
	panic("implement me")
}

func (m *stateMachine) Close() error {
	return nil
}
