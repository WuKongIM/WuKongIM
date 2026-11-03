//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// mixedSendFailureCapture owns only closed error categories and synthetic
// benchmark sequences. Original fields, errors and their object graphs never persist.
type mixedSendFailureCapture struct {
	path     string
	mu       sync.Mutex
	observed uint64
	kept     int
	records  [32]mixedSendFailureRecord
}

type mixedSendFailureRecord struct {
	NodeID    uint64   `json:"node_id"`
	ClientSeq uint64   `json:"synthetic_client_seq"`
	Causes    []string `json:"causes"`
	Stage     string   `json:"stage"`
}

func newMixedSendFailureCapture(path string) *mixedSendFailureCapture {
	return &mixedSendFailureCapture{path: path}
}

func (c *mixedSendFailureCapture) logger(node uint64) wklog.Logger {
	if c.path == "" {
		return wklog.NewNop()
	}
	return &mixedSendFailureLogger{Logger: wklog.NewNop(), capture: c, node: node}
}

type mixedSendFailureLogger struct {
	wklog.Logger
	capture *mixedSendFailureCapture
	node    uint64
}

func (l *mixedSendFailureLogger) Named(string) wklog.Logger        { return l }
func (l *mixedSendFailureLogger) With(...wklog.Field) wklog.Logger { return l }
func (*mixedSendFailureLogger) DebugEnabled() bool                 { return false }

// Warn captures only the existing gateway failure event. Successful traffic
// adds no clocks, profiles, goroutines, field retention or debug construction.
func (l *mixedSendFailureLogger) Warn(_ string, fields ...wklog.Field) {
	if len(fields) > 128 || l.node == 0 || l.node > 3 {
		return
	}
	var event, number string
	var cause error
	stage := "unknown"
	for _, field := range fields {
		switch field.Key {
		case "event":
			event, _ = field.Value.(string)
		case "clientMsgNo":
			number, _ = field.Value.(string)
		case "error":
			cause, _ = field.Value.(error)
		case "failedStage":
			value, _ := field.Value.(string)
			switch value {
			case "permission":
				stage = "permission"
			case "pre_append":
				stage = "pre_append"
			case "submitter":
				stage = "submitter"
			}
		}
	}
	if event != "internal.access.gateway.send_failed" || len(number) > 32 {
		return
	}
	base := uint64(1_000_000)
	raw, ok := strings.CutPrefix(number, "mixed-warm-")
	if ok {
		base = 0
	} else if raw, ok = strings.CutPrefix(number, "mixed-"); !ok {
		return
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 || n > 1_000_000 || strconv.FormatUint(n, 10) != raw {
		return
	}
	c := l.capture
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observed++
	if c.kept == len(c.records) {
		return
	}
	causes := []string{}
	for _, candidate := range []struct {
		name string
		err  error
	}{
		{"route_not_ready", messageusecase.ErrRouteNotReady},
		{"permission_busy", slotproxy.ErrPermissionBusy},
		{"permission_stale_route", slotproxy.ErrReadStaleRoute},
		{"slot_no_leader", slotproxy.ErrNoLeader},
		{"slot_not_leader", slotproxy.ErrNotLeader},
		{"cluster_route_not_ready", clusterpkg.ErrRouteNotReady},
		{"cluster_no_slot_leader", clusterpkg.ErrNoSlotLeader},
		{"cluster_not_started", clusterpkg.ErrNotStarted},
		{"cluster_stopping", clusterpkg.ErrStopping},
		{"transport_dial_failed", transport.ErrDialFailed},
		{"transport_node_not_found", transport.ErrNodeNotFound},
		{"transport_stopped", transport.ErrStopped},
		{"deadline", context.DeadlineExceeded},
		{"canceled", context.Canceled},
	} {
		if errors.Is(cause, candidate.err) {
			causes = append(causes, candidate.name)
		}
	}
	if len(causes) == 0 {
		causes = append(causes, "unclassified")
	}
	c.records[c.kept] = mixedSendFailureRecord{NodeID: l.node, ClientSeq: base + n, Causes: causes, Stage: stage}
	c.kept++
}

func (c *mixedSendFailureCapture) save() error {
	if c.path == "" {
		return nil
	}
	c.mu.Lock()
	records := make([]mixedSendFailureRecord, c.kept)
	copy(records, c.records[:c.kept]) // Cause slices are immutable after publication.
	observed, dropped := c.observed, c.observed-uint64(c.kept)
	c.mu.Unlock()
	data, err := json.MarshalIndent(struct {
		Schema   string                   `json:"schema"`
		Scope    string                   `json:"scope"`
		Observed uint64                   `json:"observed"`
		Dropped  uint64                   `json:"dropped"`
		Records  []mixedSendFailureRecord `json:"records"`
	}{"mixed-send-failure-log/v1", "recognized synthetic gateway SEND failures only; absence does not prove success", observed, dropped, records}, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return fmt.Errorf("mixed SEND failure evidence exceeds 64 KiB")
	}
	file, err := os.OpenFile(c.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}
