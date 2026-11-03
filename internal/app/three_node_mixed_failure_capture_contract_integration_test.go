//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestMixedSendFailureCaptureKeepsClosedCausesAndDropsPrivateFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failures.json")
	capture := newMixedSendFailureCapture(path)
	logger := capture.logger(2).Named("frame").With(wklog.UID("private-context"))
	if wklog.DebugEnabled(logger) {
		t.Fatal("failure capture enabled success-path debug work")
	}
	logger.Warn("gateway send failed", wklog.Event("internal.access.gateway.send_failed"),
		wklog.ClientMsgNo("mixed-37"), wklog.UID("private-user"), wklog.ChannelID("private-channel"),
		wklog.Error(fmt.Errorf("private-error: %w: %w", messageusecase.ErrRouteNotReady, slotproxy.ErrPermissionBusy)))
	logger.Warn("unrelated", wklog.Event("other.event"), wklog.ClientMsgNo("mixed-38"), wklog.Error(slotproxy.ErrNoLeader))
	logger.Warn("gateway send failed", wklog.Event("internal.access.gateway.send_failed"), wklog.ClientMsgNo("private-message"), wklog.Error(slotproxy.ErrNoLeader))
	if err := capture.save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-") || len(data) > 64<<10 {
		t.Fatal("capture leaked input text or exceeded its artifact bound")
	}
	var got struct {
		Observed uint64 `json:"observed"`
		Records  []struct {
			NodeID    uint64   `json:"node_id"`
			ClientSeq uint64   `json:"synthetic_client_seq"`
			Causes    []string `json:"causes"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Observed != 1 || len(got.Records) != 1 {
		t.Fatalf("capture retained %d records from %d events, want one exact SEND failure", len(got.Records), got.Observed)
	}
	r := got.Records[0]
	if r.NodeID != 2 || r.ClientSeq != 1_000_037 || strings.Join(r.Causes, ",") != "route_not_ready,permission_busy" {
		t.Fatalf("closed failure binding = %+v", r)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("failure evidence is not a private file")
	}
}

func TestMixedSendFailureCaptureBoundsConcurrentUnknownErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failures.json")
	capture := newMixedSendFailureCapture(path)
	logger := capture.logger(1)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := 0; i < 10; i++ {
				logger.Warn("gateway send failed", wklog.Event("internal.access.gateway.send_failed"),
					wklog.ClientMsgNo("mixed-warm-12"), wklog.Error(fmt.Errorf("private-unclassified")))
			}
		}()
	}
	wait.Wait()
	if err := capture.save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Observed uint64 `json:"observed"`
		Dropped  uint64 `json:"dropped"`
		Records  []struct {
			ClientSeq uint64   `json:"synthetic_client_seq"`
			Causes    []string `json:"causes"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Observed != 80 || got.Dropped != 48 || len(got.Records) != 32 || len(data) > 64<<10 {
		t.Fatalf("capture bounds = observed:%d dropped:%d retained:%d bytes:%d", got.Observed, got.Dropped, len(got.Records), len(data))
	}
	for _, record := range got.Records {
		if record.ClientSeq != 12 || strings.Join(record.Causes, ",") != "unclassified" {
			t.Fatalf("unknown error was invented or sequence changed: %+v", record)
		}
	}
}
