package app

import (
	"context"
	"strconv"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// mqttPostCommitWake adapts durable Channel appends to volatile delivery hints.
// The source key matches cursor discovery; no payload or send proof is retained.
type mqttPostCommitWake struct{ notify func(string) }

func (e mqttPostCommitWake) EnqueuePersistAfter(_ context.Context, event channelappend.CommittedEnvelope) {
	if e.notify != nil && event.MessageSeq != 0 && event.ChannelID != "" {
		e.notify(strconv.Itoa(int(event.ChannelType)) + ":" + event.ChannelID)
	}
}

// wakeMQTTSource can be bound before MQTT construction. By admission time the
// scheduler exists; constructor/Stop misses remain harmless polling hints.
func (a *App) wakeMQTTSource(source string) {
	a.mqtt.wakeSource(source)
}

// mqttReplayWake repeats an early commit wake only after a timely confirmed
// anchor. Native durability alone cannot make replay content consumer-ready.
type mqttReplayWake struct {
	stepper runtime.ReplayStepper
	notify  func(string)
}

func (w mqttReplayWake) Step(ctx context.Context, source meta.MQTTBindingOwner, cursor sessioncase.ReplayCursor) (sessioncase.ReplayStepResult, error) {
	out, err := w.stepper.Step(ctx, source, cursor)
	if err == nil && ctx.Err() == nil && out.Anchored && w.notify != nil {
		w.notify(source.ID)
	}
	return out, err
}
