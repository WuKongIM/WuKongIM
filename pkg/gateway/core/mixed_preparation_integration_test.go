//go:build integration

package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/protocol"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gt "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

type keyedPreparationProbe struct{ *deferredProbeHandler }

func (h *keyedPreparationProbe) SendOrderKey(_ session.Session, id string, kind uint8) gt.SendOrderKey {
	return gt.SendOrderKey{ID: id, Type: kind}
}

type heldPacketCallback struct {
	gt.PacketHandler
	entered chan int
	release chan struct{}
}

func (p *heldPacketCallback) OnPacket(_ gt.Context, value any) error {
	p.entered <- value.(int)
	<-p.release
	return nil
}

// A protocol callback can hold its mailbox while two independent WK keys
// collide elsewhere. The callback, its real credits and WK order are separate.
func TestMixedPacketAndColdChannelPreparation(t *testing.T) {
	packets := &heldPacketCallback{entered: make(chan int, 3), release: make(chan struct{})}
	h := &keyedPreparationProbe{&deferredProbeHandler{calls: make(chan deferredProbeCall, 2)}}
	releaseA := make(chan struct{})
	h.beforeReturn = func(c deferredProbeCall) error {
		if c.items[0].Frame.ChannelID == "a" {
			<-releaseA
		}
		if err := c.publish(0, func() error { return nil }); err != nil {
			return err
		}
		c.complete(nil)
		return nil
	}
	opts := gt.RuntimeOptions{AsyncSendWorkers: 4, AsyncSendQueueCapacity: 32, AsyncPacketMaxBytes: 64}
	s := &Server{dispatcher: newDispatcher(h), options: gt.Options{DefaultSession: gt.SessionOptions{AsyncSendBatchMaxRecords: 1}, Runtime: opts, PacketHandler: packets}}
	e, err := newSendExecutor(s, opts)
	require.NoError(t, err)
	t.Cleanup(e.stop)
	var release sync.Once
	unblock := func() { release.Do(func() { close(releaseA); close(packets.release) }) }
	t.Cleanup(unblock)
	p := deferredTestState(s, 15)
	w := deferredTestState(s, 1)
	packet := func(i int) bool { return e.submitPacket(p, protocol.InboundPacket{Value: i, Bytes: 1}) }
	require.True(t, packet(0))
	select {
	case <-packets.entered:
	case <-time.After(time.Second):
		t.Fatal("packet callback did not enter")
	}
	require.True(t, e.submit(w, "", &frame.SendPacket{ChannelID: "a", ChannelType: 1}))
	deferredNext(t, h.deferredProbeHandler)
	// "a" and "q" have identical low four FNV-1a bits. Home 15 is
	// reserved for the independent packet session in this fixture.
	require.True(t, e.submit(w, "", &frame.SendPacket{ChannelID: "q", ChannelType: 1}))
	require.True(t, packet(1), "first pending packet must fit its own home budget")
	require.True(t, packet(2), "unrelated WK preparation must not consume the packet's second home credit")
	require.False(t, packet(3), "the original two-record home limit still applies")
	select {
	case call := <-h.calls:
		require.Equal(t, "q", call.items[0].Frame.ChannelID)
	case <-time.After(time.Second):
		t.Fatal("WK preparation waited behind an unrelated packet callback")
	}
	require.EqualValues(t, 3, s.packetBytes.Load())
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, e.drain(ctx))
	require.Equal(t, 1, <-packets.entered)
	require.Equal(t, 2, <-packets.entered)
	require.Zero(t, s.packetBytes.Load())
}

type releaseObserver struct {
	recordingAsyncSendObserver
	armed atomic.Bool
	fired chan struct{}
}

func (o *releaseObserver) OnAsyncSendQueue(event gt.AsyncSendQueueEvent) {
	if o.armed.CompareAndSwap(true, false) {
		close(o.fired)
		panic("controlled queue observer failure")
	}
}

// An optional observer can fail exactly once after an inline completion.
// Later work on that key must still execute, and drain must join both records.
func TestSingleSendLaneSurvivesReleaseObserverPanic(t *testing.T) {
	observer := &releaseObserver{fired: make(chan struct{})}
	h := &keyedPreparationProbe{&deferredProbeHandler{calls: make(chan deferredProbeCall, 2)}}
	h.beforeReturn = func(call deferredProbeCall) error {
		if err := call.publish(0, func() error { return nil }); err != nil {
			return err
		}
		call.complete(nil)
		if call.items[0].Frame.ClientMsgNo == "first" {
			observer.armed.Store(true)
		}
		return nil
	}
	opts := gt.RuntimeOptions{AsyncSendWorkers: 2, AsyncSendQueueCapacity: 16, AsyncPoolReleaseTimeout: 200 * time.Millisecond}
	s := &Server{dispatcher: newDispatcher(h), options: gt.Options{DefaultSession: gt.SessionOptions{AsyncSendBatchMaxRecords: 1}, Runtime: opts, Observer: observer}}
	e, err := newSendExecutor(s, opts)
	require.NoError(t, err)
	t.Cleanup(e.stop)
	state := deferredTestState(s, 1)
	submit := func(id string) {
		require.True(t, e.submit(state, "", &frame.SendPacket{ChannelID: "shared", ChannelType: 1, ClientMsgNo: id}))
	}
	submit("first")
	deferredNext(t, h.deferredProbeHandler)
	select {
	case <-observer.fired:
	case <-time.After(time.Second):
		t.Fatal("release observation not reached")
	}
	submit("second")
	select {
	case call := <-h.calls:
		require.Equal(t, "second", call.items[0].Frame.ClientMsgNo)
	case <-time.After(time.Second):
		t.Fatal("later accepted SEND stranded after release observation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, e.drain(ctx))
	require.Zero(t, e.depth())
}
