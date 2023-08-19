package wraft

import (
	"path"
	"time"
)

type RaftNodeConfig struct {
	ID            uint64
	Addr          string
	RootDir       string
	Heartbeat     time.Duration
	Transport     Transporter
	Peers         []*Peer // peers is the list of raft peers
	ElectionTicks int
	MaxSizePerMsg uint64 // The max throughput of etcd will not exceed 100MB/s (100K * 1KB value).
	// Assuming the RTT is around 10ms, 1MB max size is large enough.

	MaxInflightMsgs int
	// max number of in-flight snapshot messages wukongim allows to have
	// This number is more than enough for most clusters with 5 machines.
	MaxInFlightMsgSnap int
	PreVote            bool
	Storage            Storage
	// MetaDBPath is the path to the metadata boltdb path.
	MetaDBPath string
	// LogWALPath is the path to WAL path.
	LogWALPath string

	monitor Monitor
	// cluster auth token
	Token string

	ClusterStorePath string
}

func NewRaftNodeConfig() *RaftNodeConfig {
	rootDir := "raftdata"
	return &RaftNodeConfig{
		Addr:               "tcp://127.0.0.1:11110",
		RootDir:            rootDir,
		Heartbeat:          100 * time.Millisecond,
		ElectionTicks:      10,
		Peers:              make([]*Peer, 0),
		MaxSizePerMsg:      1 * 1024 * 1024,
		MaxInflightMsgs:    4096 / 8,
		PreVote:            true,
		MaxInFlightMsgSnap: 16,
		MetaDBPath:         path.Join(rootDir, "meta.db"),
		LogWALPath:         path.Join(rootDir, "wal"),
		ClusterStorePath:   path.Join(rootDir, "clusterconfig"),
	}
}

func (r *RaftNodeConfig) Monitor() Monitor {
	if r.monitor == nil {
		r.monitor = &emptyMonitor{}
	}
	return r.monitor
}

func (r *RaftNodeConfig) SetMonitor(monitor Monitor) {
	r.monitor = monitor
}

func (r *RaftNodeConfig) ReqTimeout() time.Duration {
	// 5s for queue waiting, computation and disk IO delay
	// + 2 * election timeout for possible leader election
	return 5*time.Second + 2*time.Duration(r.ElectionTicks)*r.Heartbeat
}
