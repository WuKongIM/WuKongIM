package app

import (
	"time"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
)

const channelMetaBootstrapLease = runtimechannelmeta.BootstrapLease

func runtimeMetaLeaseNeedsRenewal(leaseUntilMS int64, now time.Time, renewBefore time.Duration) bool {
	return runtimechannelmeta.MetaLeaseNeedsRenewal(leaseUntilMS, now, renewBefore)
}
