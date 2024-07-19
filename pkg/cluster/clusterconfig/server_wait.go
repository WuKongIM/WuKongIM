package clusterconfig

import (
	"context"
	"time"
)

func (s *Server) MustWaitConfigVersion(version uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	for {
		if s.Config().Version >= version {
			return
		}
		select {
		case <-ctx.Done():
			panic("wait config version timeout")
		case <-time.After(time.Millisecond * 50):
		}
	}
}
