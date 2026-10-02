package app

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

func TestMQTTInboxAppenderRequiresCompleteComposition(t *testing.T) {
	for name, a := range map[string]*App{
		"missing-cluster":   {mqttInboxWrites: true},
		"partial-cluster":   {mqttInboxWrites: true, cluster: &personDirectoryLifecycleCluster{}},
		"missing-projector": {mqttInboxWrites: true, cluster: &cluster.Node{}},
		"injected-appender": {mqttInboxWrites: true, cluster: &cluster.Node{}, channelAppends: &channelappend.Group{}},
	} {
		t.Run(name, func(t *testing.T) {
			err := a.wireChannelAppend(1)
			require.ErrorContains(t, err, "inbox")
		})
	}
}
