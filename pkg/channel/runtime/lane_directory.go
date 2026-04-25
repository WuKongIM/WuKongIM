package runtime

import (
	"sync"

	core "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type laneDirectory struct {
	mu sync.RWMutex

	channelTargets map[core.ChannelKey][]PeerLaneKey
	sessions       map[PeerLaneKey]*LeaderLaneSession
}

func newLaneDirectory() *laneDirectory {
	return &laneDirectory{
		channelTargets: make(map[core.ChannelKey][]PeerLaneKey),
		sessions:       make(map[PeerLaneKey]*LeaderLaneSession),
	}
}

func (d *laneDirectory) SetReplicationTargets(key core.ChannelKey, targets []PeerLaneKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(targets) == 0 {
		delete(d.channelTargets, key)
		return
	}
	d.channelTargets[key] = append([]PeerLaneKey(nil), targets...)
}

func (d *laneDirectory) ReplicationTargets(key core.ChannelKey) []PeerLaneKey {
	d.mu.RLock()
	defer d.mu.RUnlock()
	targets := d.channelTargets[key]
	return append([]PeerLaneKey(nil), targets...)
}

func (d *laneDirectory) RegisterSession(key PeerLaneKey, session *LeaderLaneSession) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if session == nil {
		delete(d.sessions, key)
		return
	}
	d.sessions[key] = session
}

func (d *laneDirectory) Session(key PeerLaneKey) (*LeaderLaneSession, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	session, ok := d.sessions[key]
	return session, ok
}
