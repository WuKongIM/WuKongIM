package replication

import (
	"fmt"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"sync/atomic"
)

var fixtureConflictCount atomic.Int64

func fixtureConflict(site string) error {
	if fixtureConflictCount.Add(1) <= 16 {
		fmt.Printf("FIXTURE_CONFLICT replication %s\n", site)
	}
	return ch.ErrLogConflict
}
