package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestWillPreparationRecoversLostPhaseReplyWithoutRepeatingHooks(t *testing.T) {
	for _, lost := range []meta.MQTTWillDispatchStage{meta.MQTTWillDispatchPreparing, meta.MQTTWillDispatchPrepared, meta.MQTTWillDispatchStarted} {
		t.Run(map[meta.MQTTWillDispatchStage]string{1: "claim", 2: "prepared", 3: "started"}[lost], func(t *testing.T) {
			f := newWillExecutionFixture(t)
			prepared := 0
			p := f.opts.Publications.(willPublications)
			p.prepare = func(_ context.Context, _ app.WillPublication) ([]byte, error) {
				prepared++
				return []byte("frozen body"), nil
			}
			p.publish = func(_ context.Context, q app.WillPublication) error {
				f.published++
				require.Equal(t, "frozen body", string(q.Payload))
				require.Equal(t, meta.MQTTWillDispatchStarted, f.read(t).DispatchStage)
				f.found = true
				return nil
			}
			f.opts.Publications = p
			f.store.after = func(w meta.MQTTWill, r meta.MQTTWillResult) (meta.MQTTWillResult, error) {
				if w.DispatchStage == lost {
					return meta.MQTTWillResult{}, errors.New("phase observation lost")
				}
				return r, nil
			}
			_, err := f.executor(t).Execute(context.Background(), f.key)
			require.Error(t, err)
			require.Zero(t, f.published)
			f.store.after = nil
			f.now = f.now.Add(11 * time.Second)
			r, err := f.executor(t).Execute(context.Background(), f.key)
			if lost == meta.MQTTWillDispatchStarted {
				require.ErrorIs(t, err, app.ErrWillPending)
				require.Zero(t, f.published)
			} else {
				require.NoError(t, err)
				require.Equal(t, meta.MQTTWillPublished, r.Stage)
				require.Equal(t, 1, f.published)
			}
			require.Equal(t, 1, prepared)
		})
	}
}

func TestWillPreparationRecoveryUsesFrozenBodyAfterLostPublication(t *testing.T) {
	f := newWillExecutionFixture(t)
	p := f.opts.Publications.(willPublications)
	prepared := 0
	p.prepare = func(context.Context, app.WillPublication) ([]byte, error) {
		prepared++
		return []byte("hook result"), nil
	}
	p.publish = func(_ context.Context, q app.WillPublication) error {
		f.published++
		require.Equal(t, "hook result", string(q.Payload))
		return errors.New("unknown append")
	}
	p.lookup = func(_ context.Context, q app.WillPublication) (app.WillPublicationReceipt, bool, error) {
		require.Equal(t, "hook result", string(q.Payload))
		return f.receipt, true, nil
	}
	// First retain an uncertain Started row, then recover its positive proof.
	lookup := p.lookup
	p.lookup = func(context.Context, app.WillPublication) (app.WillPublicationReceipt, bool, error) {
		return app.WillPublicationReceipt{}, false, nil
	}
	f.opts.Publications = p
	_, err := f.executor(t).Execute(context.Background(), f.key)
	require.Error(t, err)
	f.now = f.now.Add(11 * time.Second)
	f.authErr = app.ErrWillDenied
	p.lookup = lookup
	f.opts.Publications = p
	r, err := f.executor(t).Execute(context.Background(), f.key)
	require.NoError(t, err)
	require.Equal(t, f.receipt, r.Receipt)
	require.Equal(t, 1, prepared)
	require.Equal(t, 1, f.published)
}
