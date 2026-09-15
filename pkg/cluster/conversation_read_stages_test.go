package cluster

import (
	"context"
	"errors"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"testing"
	"time"
)

type nodeReadStageProbe struct {
	reactor.Observer
	events []string
}

func (p *nodeReadStageProbe) ObserveConversationReadStage(scope, stage, result string, d time.Duration) {
	p.events = append(p.events, scope+"/"+stage+"/"+result)
}

type stageHeadReader struct {
	channelService
	err     error
	itemErr error
}

func (r stageHeadReader) ReadConversationHeads(ctx context.Context, ids []ch.ChannelID, uid string, badges ...channels.ConversationBadgeQuery) ([]channels.ConversationHeadResult, error) {
	if r.err != nil {
		return nil, r.err
	}
	return []channels.ConversationHeadResult{{Err: r.itemErr}}, nil
}
func (r stageHeadReader) ReadPersistedConversationHeads(ctx context.Context, ids []ch.ChannelID, uid string, badges ...channels.ConversationBadgeQuery) ([]channels.ConversationHeadResult, error) {
	return r.ReadConversationHeads(ctx, ids, uid, badges...)
}
func TestNodeConversationReadStagesPreserveErrorsAndSkipUnattemptedOverlay(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		for _, mode := range []string{"ok", "metadata", "heads", "item", "cancel"} {
			t.Run(mode+map[bool]string{true: "/persisted", false: "/committed"}[persisted], func(t *testing.T) {
				n, _ := newLocalMetadataScanNode(t)
				probe := &nodeReadStageProbe{}
				n.cfg.Channel.Observer = probe
				reader := stageHeadReader{}
				sentinel := errors.New("head error")
				if mode == "heads" {
					reader.err = sentinel
				}
				if mode == "item" {
					reader.itemErr = sentinel
				}
				n.channels = reader
				if mode == "metadata" {
					n.defaultSlotMetaDB = nil
				}
				ctx := context.Background()
				if mode == "cancel" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				result, err := n.readChannelConversationHeads(ctx, []ch.ChannelID{{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}}, "u", persisted)
				if mode == "cancel" {
					if !errors.Is(err, context.Canceled) || len(probe.events) != 0 {
						t.Fatalf("err=%v events=%v", err, probe.events)
					}
					return
				}
				scope := "committed_heads"
				if persisted {
					scope = "persisted_heads"
				}
				if mode == "metadata" {
					if len(probe.events) != 1 || probe.events[0] != scope+"/metadata/error" || len(result) != 1 || result[0].Err == nil {
						t.Fatalf("result=%v events=%v", result, probe.events)
					}
					return
				}
				want := "ok"
				if mode == "heads" || mode == "item" {
					want = "error"
				}
				if len(probe.events) != 2 || probe.events[0] != scope+"/metadata/ok" || probe.events[1] != scope+"/heads/"+want {
					t.Fatalf("events=%v", probe.events)
				}
				if mode == "heads" && !errors.Is(err, sentinel) {
					t.Fatalf("err=%v", err)
				}
				if mode == "item" && (len(result) != 1 || !errors.Is(result[0].Err, sentinel)) {
					t.Fatalf("result=%v", result)
				}
			})
		}
	}
	timer := (&Node{}).conversationReadTimer(true)
	if !timer.start().IsZero() {
		t.Fatal("disabled observer reads clock")
	}
	if n := testing.AllocsPerRun(100, func() { timer.finish("heads", timer.start(), false) }); n != 0 {
		t.Fatalf("allocations=%v", n)
	}
}

func (p *nodeReadStageProbe) ConversationReadStageObservationEnabled() bool { return true }
