//go:build integration

package mqttsession_test

import (
	"context"
	"testing"
	"time"

	appendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTInboxAppenderBoundsPendingDirectoryWait(t *testing.T) {
	for _, mode := range []string{"turns", "timeout", "cancel-wait", "cancel-admission"} {
		t.Run(mode, func(t *testing.T) {
			f, s, options := setupInboxAdmission(t)
			require.NoError(t, s.db.HashSlot(7).DeleteChannel(context.Background(), f.channel.ID, 1))
			admission, err := app.NewInboxAdmission(options)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			directoryCalls, appendCalls, admissionReads := 0, 0, 0
			s.reads = func(readCtx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				if q.Kind == meta.MQTTReadInboxAdmission {
					admissionReads++
				}
				return s.groupSourceStore.ReadMQTT(readCtx, q)
			}
			admitted := make(chan struct{})
			o := app.InboxAppenderOptions{Admission: admission, Attempts: 3, Retry: time.Millisecond, Timeout: time.Second,
				Directory: inboxAppendDirectory(func(context.Context, app.SourceChannel) error {
					directoryCalls++
					close(admitted)
					if mode == "cancel-admission" {
						cancel()
					}
					return nil
				}), Next: inboxAppendNext(func(context.Context, appendcontract.AppendBatchRequest) (appendcontract.AppendBatchResult, error) {
					appendCalls++
					return appendcontract.AppendBatchResult{}, nil
				})}
			want := app.ErrInboxAdmissionPending
			if mode == "timeout" {
				o.Retry, o.Timeout = time.Second, 20*time.Millisecond
				want = context.DeadlineExceeded
			}
			if mode == "cancel-admission" || mode == "cancel-wait" {
				want = context.Canceled
			}
			if mode == "cancel-wait" {
				o.Retry = time.Second
			}
			p, err := app.NewInboxAppender(o)
			require.NoError(t, err)
			finished := make(chan error, 1)
			go func() {
				_, err := p.AppendBatch(ctx, inboxAppendRequest(f))
				finished <- err
			}()
			select {
			case <-admitted:
			case <-time.After(2 * time.Second):
				t.Fatal("directory admission did not start")
			}
			if mode == "cancel-wait" {
				cancel()
			}
			select {
			case err := <-finished:
				require.ErrorIs(t, err, want)
				require.ErrorIs(t, err, appendcontract.ErrRouteNotReady)
			case <-time.After(2 * time.Second):
				t.Fatal("bounded preparation did not exit")
			}
			require.Equal(t, 1, directoryCalls)
			require.Zero(t, appendCalls)
			require.Zero(t, f.protectCalls)
			if mode == "turns" {
				require.Equal(t, 3, admissionReads)
			}
		})
	}
}
