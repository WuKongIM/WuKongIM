package app

import (
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"testing"
	"time"
)

type compositionReadStageProbe struct {
	*channelCompositionProbe
	calls int
}

func (p *compositionReadStageProbe) ObserveConversationReadStage(string, string, string, time.Duration) {
	p.calls++
}
func (p *compositionReadStageProbe) ObserveMessageUpdateReadStage(string, string, time.Duration) {
	p.calls++
}
func TestConversationReadStageComposition(t *testing.T) {
	reg := metrics.New(1, "node")
	custom := &compositionReadStageProbe{channelCompositionProbe: newChannelCompositionProbe()}
	combined := combineChannelObservers(custom, channelMetricsObserver{metrics: reg})
	combined.(cluster.ConversationReadStageObserver).ObserveConversationReadStage("persisted_heads", "heads", "ok", time.Second)
	combined.(proxy.MessageUpdateReadObserver).ObserveMessageUpdateReadStage("barrier", "error", time.Second)
	conversationListMetricsObserver{metrics: reg}.ObserveConversationReadStage("list", "response", "ok", time.Second)
	if custom.calls != 2 {
		t.Fatalf("custom calls=%d", custom.calls)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var count uint64
	for _, family := range families {
		if family.GetName() == "wukongim_conversation_read_stage_duration_seconds" {
			for _, metric := range family.Metric {
				count += metric.GetHistogram().GetSampleCount()
			}
		}
	}
	if count != 3 {
		t.Fatalf("metric count=%d", count)
	}
	conversationListMetricsObserver{}.ObserveConversationReadStage("list", "response", "ok", 0)
	channelMetricsObserver{}.ObserveMessageUpdateReadStage("barrier", "ok", 0)
	channelMetricsObserver{}.ObserveConversationReadStage("persisted_heads", "heads", "ok", 0)
}

func (p *compositionReadStageProbe) ConversationReadStageObservationEnabled() bool { return true }

func (p *compositionReadStageProbe) MessageUpdateReadObservationEnabled() bool { return true }

func TestConversationReadStagesDisabledWithTopAndLegacyObservers(t *testing.T) {
	for _, top := range []bool{false, true} {
		a := &App{cfg: Config{Top: TopConfig{APIEnabled: top, CollectInterval: time.Second, HistoryWindow: time.Minute}}}
		cfg := cluster.Config{NodeID: 1}
		cfg.Channel.Observer = newChannelCompositionProbe()
		a.configureObservability(&cfg)
		if a.metrics != nil {
			t.Fatal("metrics enabled unexpectedly")
		}
		if o, ok := cfg.Channel.Observer.(cluster.ConversationReadStageObserver); ok && o.ConversationReadStageObservationEnabled() {
			t.Fatal("inert composite enables conversation timing")
		}
		if o, ok := cfg.Channel.Observer.(proxy.MessageUpdateReadObserver); ok && o.MessageUpdateReadObservationEnabled() {
			t.Fatal("inert composite enables edit timing")
		}
		if a.conversationListObserver() != nil {
			t.Fatal("disabled API observer installed")
		}
	}
}
