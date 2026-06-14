package bench

import (
	"fmt"
	"math/rand"
)

type benchChannel struct {
	ID   string
	Type uint8
}

type channelPlanner struct {
	channels []benchChannel
	pick     string
	seed     int64
}

func newChannelPlanner(cfg sendConfig) (*channelPlanner, error) {
	if cfg.Channels <= 0 {
		return nil, fmt.Errorf("--channels must be greater than zero")
	}
	channels := make([]benchChannel, cfg.Channels)
	for i := 0; i < cfg.Channels; i++ {
		id := cfg.Channel
		if cfg.Channels > 1 {
			id = fmt.Sprintf("%s-%06d", cfg.ChannelPrefix, i+1)
		}
		if cfg.ChannelType == "cmd" {
			id = commandChannelID(id)
		}
		channels[i] = benchChannel{ID: id, Type: cfg.ChannelTypeID}
	}
	return &channelPlanner{channels: channels, pick: cfg.ChannelPick, seed: cfg.RandomSeed}, nil
}

func (p *channelPlanner) Pick(offset int) benchChannel {
	if len(p.channels) == 0 {
		return benchChannel{}
	}
	if p.pick == channelPickRandom {
		seed := p.seed
		if seed == 0 {
			seed = 1
		}
		r := rand.New(rand.NewSource(seed + int64(offset)))
		return p.channels[r.Intn(len(p.channels))]
	}
	return p.channels[offset%len(p.channels)]
}

func (p *channelPlanner) ChannelIDs() []string {
	out := make([]string, 0, len(p.channels))
	for _, ch := range p.channels {
		out = append(out, ch.ID)
	}
	return out
}
