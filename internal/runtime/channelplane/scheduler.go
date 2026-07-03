package channelplane

import "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"

// scheduler tracks reactor-local channel cells that are ready to advance.
type scheduler struct {
	ready  []channel.ChannelID
	queued map[channel.ChannelID]struct{}
}

func (s *scheduler) markReady(key channel.ChannelID) {
	if key.ID == "" || key.Type == 0 {
		return
	}
	if s.queued == nil {
		s.queued = make(map[channel.ChannelID]struct{})
	}
	if _, ok := s.queued[key]; ok {
		return
	}
	s.queued[key] = struct{}{}
	s.ready = append(s.ready, key)
}

func (s *scheduler) pop() (channel.ChannelID, bool) {
	if len(s.ready) == 0 {
		return channel.ChannelID{}, false
	}
	key := s.ready[0]
	s.ready[0] = channel.ChannelID{}
	s.ready = s.ready[1:]
	delete(s.queued, key)
	return key, true
}
