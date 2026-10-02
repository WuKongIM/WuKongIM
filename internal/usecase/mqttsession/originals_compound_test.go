package mqttsession_test

import (
	"context"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type compoundWindowChannels struct {
	*windowFixture
	calls int
	read  func(context.Context, ch.MQTTReplayOriginalRequest) (ch.MQTTReplayOriginalResult, error)
}

func (f *compoundWindowChannels) ReadChannelMQTTOriginals(ctx context.Context, q ch.MQTTReplayOriginalRequest) (ch.MQTTReplayOriginalResult, error) {
	f.calls++
	return f.read(ctx, q)
}
func (f *compoundWindowChannels) PlanChannelMQTTReplay(context.Context, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	f.t.Fatal("compound path must not make a separate plan call")
	return ch.MQTTReplayPlan{}, nil
}
func (f *compoundWindowChannels) ReadChannelMQTTReplay(context.Context, ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error) {
	f.t.Fatal("compound path must not make a separate content call")
	return ch.MQTTReplayConsumerPage{}, nil
}

func TestWindowAdmissionCompoundOriginalsRetainOuterAuthorityAndOwnerChecks(t *testing.T) {
	for _, fault := range []string{"success", "invalid_result", "placement", "permission", "owner", "canceled", "dependency_error"} {
		t.Run(fault, func(t *testing.T) {
			f, a, _ := setupWindow(t)
			accountWindow(t, f, a)
			channels := &compoundWindowChannels{windowFixture: f}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			channels.read = func(c context.Context, q ch.MQTTReplayOriginalRequest) (ch.MQTTReplayOriginalResult, error) {
				require.NoError(t, c.Err())
				require.True(t, q.Valid())
				require.EqualValues(t, 10, q.StartAfter)
				require.EqualValues(t, 11, q.AccountedThrough)
				result := ch.MQTTReplayOriginalResult{Plan: f.plan, Page: f.page}
				switch fault {
				case "invalid_result":
					result.Page.After.Digest[0]++
				case "placement":
					f.placement.RouteGeneration++
				case "permission":
					f.version++
				case "owner":
					require.NoError(t, f.owners.Fence(f.connection.Owner))
				case "canceled":
					cancel()
				case "dependency_error":
					return result, ch.ErrNotReady
				}
				return result, nil
			}
			w, e := app.NewWindowAdmission(app.WindowAdmissionOptions{Store: f, Owners: f.owners, Metadata: f, Channels: channels, Authorization: f.options.Authorization, Now: func() time.Time { return f.now }})
			require.NoError(t, e)
			got, e := w.Prepare(ctx, f.connection.Owner, f.key)
			require.Equal(t, 1, channels.calls)
			if fault == "success" {
				require.NoError(t, e)
				require.NotNil(t, got.Delivery)
				require.Equal(t, 1, f.window.writes)
			} else {
				require.Error(t, e)
				require.Nil(t, got.Delivery)
				require.Zero(t, f.window.writes)
			}
		})
	}
}
