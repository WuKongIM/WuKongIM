package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"strings"
	"testing"
)

type hintSession struct{ events []*frame.EventPacket }

func (s *hintSession) WriteDelivery(v any) error {
	s.events = append(s.events, v.(*frame.EventPacket))
	return nil
}
func (*hintSession) CloseSession(string) error { return nil }
func TestMessageUpdateHintCapabilityAndExactOwner(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 1})
	session := &hintSession{}
	route := registerLocalSessionWriterTestSession(t, registry, 1, "alice", 10, session)
	writer := &MessageUpdateHints{Online: registry, NodeID: 1}
	hint := message.MessageUpdateHint{ChannelID: channelid.EncodePersonChannel("alice", "bob"), ChannelType: 1, MessageID: 1, MessageSeq: 2, Version: 3}
	send := func(r onlinedelivery.Route) {
		t.Helper()
		if err := writer.WriteMessageUpdateHints(context.Background(), []onlinedelivery.Route{r}, hint); err != nil {
			t.Fatal(err)
		}
	}
	send(route)
	if len(session.events) != 0 {
		t.Fatal("old SDK received EVENT")
	}
	if err := registry.EnableMessageUpdates("alice", 10, true); err != nil {
		t.Fatal(err)
	}
	stale := route
	stale.OwnerSeq++
	send(stale)
	if len(session.events) != 0 {
		t.Fatal("stale owner received EVENT")
	}
	send(route)
	if len(session.events) != 1 || session.events[0].Type != "message_updated" {
		t.Fatal(session.events)
	}
	var body map[string]any
	if err := json.Unmarshal(session.events[0].Data, &body); err != nil {
		t.Fatal(err)
	}
	if body["channel_id"] != "bob" || body["version"] != "3" || body["payload"] != nil {
		t.Fatal(body)
	}
	if err := registry.EnableMessageUpdates("alice", 10, false); err != nil {
		t.Fatal(err)
	}
	send(route)
	if len(session.events) != 1 {
		t.Fatal("disabled capability was ignored")
	}
}

type hintPresenceMap map[string][]presenceusecase.Route

func (p hintPresenceMap) EndpointsByUIDs(context.Context, []string) (map[string][]presenceusecase.Route, error) {
	return p, nil
}

type hintFramePeer struct{ calls, routes int }

func (p *hintFramePeer) CallRPC(_ context.Context, _ uint64, _ uint8, body []byte) ([]byte, error) {
	if len(body) > 256<<10 {
		return nil, fmt.Errorf("oversize frame: %d", len(body))
	}
	var q accessnode.MessageUpdateDelivery
	if err := json.Unmarshal(body, &q); err != nil {
		return nil, err
	}
	p.calls++
	p.routes += len(q.Routes)
	return []byte{1}, nil
}
func TestMessageUpdateHintSplitsEscapedUIDFrames(t *testing.T) {
	presence := hintPresenceMap{}
	uids := make([]string, 128)
	for i := range uids {
		uid := fmt.Sprintf("%s%d", strings.Repeat("\n", 2048), i)
		uids[i] = uid
		presence[uid] = []presenceusecase.Route{{UID: uid, OwnerNodeID: 2, SessionID: uint64(i + 1)}}
	}
	peer := &hintFramePeer{}
	h := &MessageUpdateHints{Presence: presence, Peers: peer, NodeID: 1}
	if err := h.SendMessageUpdateHint(context.Background(), uids, message.MessageUpdateHint{ChannelID: "g", ChannelType: 2, MessageID: 1, MessageSeq: 1, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if peer.routes != 128 || peer.calls < 2 {
		t.Fatalf("calls=%d routes=%d", peer.calls, peer.routes)
	}
	// One unrepresentable advisory hint must not stall progress for everyone else.
	uid := strings.Repeat("\x00", 65535)
	presence[uid] = []presenceusecase.Route{{UID: uid, OwnerNodeID: 2, SessionID: 1}}
	if err := h.SendMessageUpdateHint(context.Background(), []string{uid}, message.MessageUpdateHint{ChannelID: "g", ChannelType: 2, MessageID: 1, MessageSeq: 1, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if peer.routes != 128 {
		t.Fatal("oversized singleton reached peer")
	}
}
