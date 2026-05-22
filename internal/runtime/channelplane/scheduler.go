package channelplane

import "github.com/WuKongIM/WuKongIM/pkg/channel"

// scheduler tracks reactor-local channel cells that are ready to advance.
type scheduler struct {
	ready  []channel.ChannelKey
	queued map[channel.ChannelKey]struct{}
}

func (s *scheduler) markReady(key channel.ChannelKey) {
	if key == "" {
		return
	}
	if s.queued == nil {
		s.queued = make(map[channel.ChannelKey]struct{})
	}
	if _, ok := s.queued[key]; ok {
		return
	}
	s.queued[key] = struct{}{}
	s.ready = append(s.ready, key)
}

func (s *scheduler) pop() (channel.ChannelKey, bool) {
	if len(s.ready) == 0 {
		return "", false
	}
	key := s.ready[0]
	s.ready[0] = ""
	s.ready = s.ready[1:]
	delete(s.queued, key)
	return key, true
}
