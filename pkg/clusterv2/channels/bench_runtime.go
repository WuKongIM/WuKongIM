package channels

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// RuntimeSnapshot returns benchmark runtime state when the wrapped runtime supports it.
func (s *Service) RuntimeSnapshot(ctx context.Context) (ch.RuntimeSnapshot, error) {
	bench, ok := s.runtime.(ch.RuntimeBench)
	if !ok {
		return ch.RuntimeSnapshot{}, ch.ErrInvalidConfig
	}
	return bench.RuntimeSnapshot(ctx)
}

// RuntimeProbe checks selected local runtime state when the wrapped runtime supports it.
func (s *Service) RuntimeProbe(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeProbeResult, error) {
	bench, ok := s.runtime.(ch.RuntimeBench)
	if !ok {
		return ch.RuntimeProbeResult{}, ch.ErrInvalidConfig
	}
	return bench.RuntimeProbe(ctx, selector)
}

// RuntimeEvict evicts selected local runtime state when the wrapped runtime supports it.
func (s *Service) RuntimeEvict(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeEvictResult, error) {
	bench, ok := s.runtime.(ch.RuntimeBench)
	if !ok {
		return ch.RuntimeEvictResult{}, ch.ErrInvalidConfig
	}
	return bench.RuntimeEvict(ctx, selector)
}
