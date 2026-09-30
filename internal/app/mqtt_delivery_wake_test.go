package app

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type mqttReplayWakeStep func(context.Context, meta.MQTTBindingOwner, sessioncase.ReplayCursor) (sessioncase.ReplayStepResult, error)

func (f mqttReplayWakeStep) Step(ctx context.Context, source meta.MQTTBindingOwner, cursor sessioncase.ReplayCursor) (sessioncase.ReplayStepResult, error) {
	return f(ctx, source, cursor)
}

func TestMQTTDeliveryWakeUsesCommittedSourceAndConfirmedAnchor(t *testing.T) {
	var sources []string
	notify := func(source string) { sources = append(sources, source) }
	e := mqttPostCommitWake{notify: notify}
	e.EnqueuePersistAfter(context.Background(), channelappend.CommittedEnvelope{ChannelID: "group:one", ChannelType: 2, MessageSeq: 42})
	e.EnqueuePersistAfter(context.Background(), channelappend.CommittedEnvelope{ChannelID: "group:one", ChannelType: 2})
	require.Equal(t, []string{"2:group:one"}, sources)
	for _, mode := range []string{"idle", "anchored", "failed", "late"} {
		ctx, cancel := context.WithCancel(context.Background())
		w := mqttReplayWake{notify: notify, stepper: mqttReplayWakeStep(func(context.Context, meta.MQTTBindingOwner, sessioncase.ReplayCursor) (sessioncase.ReplayStepResult, error) {
			if mode == "late" {
				cancel()
			}
			if mode == "failed" {
				return sessioncase.ReplayStepResult{Anchored: true}, errors.New("unconfirmed")
			}
			return sessioncase.ReplayStepResult{Anchored: mode != "idle"}, nil
		})}
		before := len(sources)
		_, _ = w.Step(ctx, meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: "2:group:one", Generation: "generation"}, sessioncase.ReplayCursor{})
		cancel()
		if mode == "anchored" {
			require.Len(t, sources, before+1)
		} else {
			require.Len(t, sources, before)
		}
	}
}
