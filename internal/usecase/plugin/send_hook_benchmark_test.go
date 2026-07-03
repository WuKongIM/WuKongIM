package plugin

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/stretchr/testify/require"
)

var benchmarkSendCommandSink message.SendCommand
var benchmarkSendReasonSink message.Reason

func BenchmarkSendPluginCandidates(b *testing.B) {
	for _, count := range []int{1, 16, 256, 1024} {
		b.Run(fmt.Sprintf("plugins_%d", count), func(b *testing.B) {
			app, err := NewApp(Options{Runtime: &recordingRuntime{plugins: benchmarkSendPlugins(count)}, Invoker: benchmarkSendInvoker{}})
			require.NoError(b, err)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				candidates, err := app.SendPluginCandidates(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if len(candidates) != count {
					b.Fatalf("candidates = %d, want %d", len(candidates), count)
				}
			}
		})
	}
}

func BenchmarkBeforeSend(b *testing.B) {
	response := mustMarshalBenchmarkSendPacket(b, &pluginproto.SendPacket{
		Payload: []byte("mutated"),
		Reason:  uint32(message.ReasonSuccess),
	})
	cases := []struct {
		name    string
		plugins []ObservedPlugin
	}{
		{name: "no_candidates"},
		{name: "one_plugin", plugins: benchmarkSendPlugins(1)},
		{name: "chain_4", plugins: benchmarkSendPlugins(4)},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			app, err := NewApp(Options{Runtime: &recordingRuntime{plugins: tc.plugins}, Invoker: benchmarkSendInvoker{response: response}})
			require.NoError(b, err)
			cmd := message.SendCommand{FromUID: "u1", DeviceID: "d1", DeviceFlag: 1, SenderSessionID: 10, ChannelID: "g1", ChannelType: 2, Payload: []byte("hello")}
			b.ReportAllocs()
			b.SetBytes(int64(len(cmd.Payload)))
			for i := 0; i < b.N; i++ {
				mutated, reason, err := app.BeforeSend(context.Background(), cmd)
				if err != nil {
					b.Fatal(err)
				}
				if reason != message.ReasonSuccess {
					b.Fatalf("reason = %v, want success", reason)
				}
				benchmarkSendCommandSink = mutated
				benchmarkSendReasonSink = reason
			}
		})
	}
}

type benchmarkSendInvoker struct {
	response []byte
}

func (i benchmarkSendInvoker) RequestPlugin(context.Context, string, string, []byte) ([]byte, error) {
	if i.response == nil {
		return mustMarshalBenchmarkSendPacket(nil, &pluginproto.SendPacket{Reason: uint32(message.ReasonSuccess)}), nil
	}
	return i.response, nil
}

func (i benchmarkSendInvoker) SendPlugin(string, uint32, []byte) error {
	return nil
}

func benchmarkSendPlugins(count int) []ObservedPlugin {
	plugins := make([]ObservedPlugin, 0, count)
	for i := 0; i < count; i++ {
		plugins = append(plugins, ObservedPlugin{
			No:       fmt.Sprintf("send-plugin-%04d", i),
			Methods:  []Method{MethodSend},
			Priority: i % 32,
			Status:   StatusRunning,
			Enabled:  true,
		})
	}
	return plugins
}

func mustMarshalBenchmarkSendPacket(tb testing.TB, packet *pluginproto.SendPacket) []byte {
	if tb != nil {
		tb.Helper()
	}
	data, err := packet.Marshal()
	if err != nil {
		if tb == nil {
			panic(err)
		}
		tb.Fatal(err)
	}
	return data
}
