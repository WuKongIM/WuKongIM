package app

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestChannelRuntimeSummaryStaysUnknownUntilEveryReactorReports(t *testing.T) {
	collector := newChannelRuntimeSummaryCollector()
	collector.SetChannelRuntimeReactorCount(2)
	collector.SetChannelRuntimeCount(0, ch.RoleLeader, 2)
	collector.SetChannelRuntimeCount(0, ch.RoleFollower, 3)

	if summary := collector.Snapshot(); !summary.Unknown {
		t.Fatalf("partial summary = %#v, want unknown", summary)
	}

	collector.SetChannelRuntimeCount(1, ch.RoleLeader, 1)
	collector.SetChannelRuntimeCount(1, ch.RoleFollower, 4)
	summary := collector.Snapshot()
	if summary.Unknown || summary.ActiveTotal != 10 || summary.ActiveLeader != 3 || summary.ActiveFollower != 7 {
		t.Fatalf("complete summary = %#v, want total=10 leader=3 follower=7 known", summary)
	}
}

func TestChannelRuntimeSummaryTreatsCompleteZeroReportsAsKnown(t *testing.T) {
	collector := newChannelRuntimeSummaryCollector()
	collector.SetChannelRuntimeReactorCount(2)
	for reactorID := 0; reactorID < 2; reactorID++ {
		collector.SetChannelRuntimeCount(reactorID, ch.RoleLeader, 0)
		collector.SetChannelRuntimeCount(reactorID, ch.RoleFollower, 0)
	}

	summary := collector.Snapshot()
	if summary.Unknown || summary.ActiveTotal != 0 || summary.ActiveLeader != 0 || summary.ActiveFollower != 0 {
		t.Fatalf("complete zero summary = %#v, want known zero", summary)
	}
}

func TestConfigureObservabilityWiresChannelRuntimeSummaryAlongsideExistingObserver(t *testing.T) {
	app := &App{}
	clusterConfig := cluster.Config{}
	clusterConfig.Channel.Observer = topChannelObserver{top: newTopCollector(topCollectorOptions{})}

	app.configureObservability(&clusterConfig)
	topology, ok := clusterConfig.Channel.Observer.(reactor.RuntimeTopologyObserver)
	if !ok {
		t.Fatalf("observer %T does not expose runtime topology", clusterConfig.Channel.Observer)
	}
	runtimeObserver, ok := clusterConfig.Channel.Observer.(reactor.RuntimeObserver)
	if !ok {
		t.Fatalf("observer %T does not expose runtime counts", clusterConfig.Channel.Observer)
	}
	topology.SetChannelRuntimeReactorCount(1)
	runtimeObserver.SetChannelRuntimeCount(0, ch.RoleLeader, 2)
	runtimeObserver.SetChannelRuntimeCount(0, ch.RoleFollower, 3)

	summary := app.channelRuntimeSummary.Snapshot()
	if summary.Unknown || summary.ActiveTotal != 5 || summary.ActiveLeader != 2 || summary.ActiveFollower != 3 {
		t.Fatalf("summary = %#v, want total=5 leader=2 follower=3 known", summary)
	}
}
