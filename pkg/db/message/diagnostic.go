// Diagnostic-only source copy for Issue #977. No product repair or qualification.
// Events exist only during an operator-owned runtime trace and have a hard cap.
package message

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"runtime/trace"
	"strconv"
	"strings"
	"sync/atomic"
)

var diagnosticEvents atomic.Uint64

func diagnosticPrepared(phase string, prepared []preparedCommitRows, lane string, batch *engine.Batch) {
	if !trace.IsEnabled() {
		return
	}
	for _, item := range prepared {
		ordinal, selected := diagnosticOrdinal(item.rows)
		if !selected {
			continue
		}
		for _, proposal := range item.proposals {
			n := diagnosticEvents.Add(1)
			if n > 2048 {
				if n == 2049 {
					trace.Log(context.Background(), "wk977.proposal", `{"phase":"cap"}`)
				}
				return
			}
			m := proposal.manifest
			group := uint64(0)
			if batch != nil {
				group = batch.DiagnosticID()
			}
			data, _ := json.Marshal(map[string]any{"phase": phase, "ordinal": ordinal, "cmd": fmt.Sprintf("%x", m.CommandID), "digest": fmt.Sprintf("%x", m.Digest), "base": m.BaseOffset, "last": m.LastOffset, "lane": lane, "group": group})
			trace.Log(context.Background(), "wk977.proposal", string(data))
		}
	}
}

func diagnosticOrdinal(rows []messageRow) (int, bool) {
	for _, record := range rows {
		suffix, ok := strings.CutPrefix(record.ClientMsgNo, "two-slots-one-remote-leader-c1-")
		if !ok {
			continue
		}
		ordinal, err := strconv.Atoi(suffix)
		if err == nil && ordinal >= 0 && ordinal < 64 && suffix == fmt.Sprintf("%03d", ordinal) {
			return ordinal, true
		}
	}
	return 0, false
}
