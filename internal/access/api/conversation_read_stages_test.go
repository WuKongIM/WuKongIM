package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type readStageEvent struct {
	scope, stage, result string
	duration             time.Duration
}
type readStageProbe struct {
	recordingConversationListObserver
	stages  []readStageEvent
	written func() bool
	t       *testing.T
}

func (p *readStageProbe) ObserveConversationReadStage(scope, stage, result string, d time.Duration) {
	if !p.written() {
		p.t.Fatal("stage observed before response write")
	}
	p.stages = append(p.stages, readStageEvent{scope, stage, result, d})
}
func TestConversationReadStagesIncludeResponseAndRejectFailedWrites(t *testing.T) {
	for _, scope := range []string{"list", "sync"} {
		for _, mode := range []string{"ok", "invalid", "usecase", "restore", "write", "cancel"} {
			t.Run(scope+"/"+mode, func(t *testing.T) {
				rec := httptest.NewRecorder()
				p := &readStageProbe{t: t, written: func() bool { return rec.Body.Len() > 0 }}
				uc := &recordingLegacyConversationSync{}
				if mode == "usecase" {
					uc.err = errors.New("read failed")
					uc.recordingConversationUsecase.err = uc.err
				}
				opts := Options{Conversations: uc, ConversationListObserver: p}
				if mode == "restore" {
					calls := 0
					opts.ContentEpoch = func(context.Context) (uint64, error) { return 1, nil }
					opts.ContentReadFence = func() (uint64, bool) { calls++; return uint64(calls), false }
				}
				body := `{"uid":"u"}`
				if mode == "invalid" {
					body = `{`
				}
				req := httptest.NewRequest(http.MethodPost, "/conversation/"+scope, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				if mode == "cancel" {
					ctx, cancel := context.WithCancel(req.Context())
					cancel()
					req = req.WithContext(ctx)
				}
				var writer http.ResponseWriter = rec
				if mode == "write" {
					writer = stageFailWriter{rec}
					p.written = func() bool { return true }
				}
				New(opts).Handler().ServeHTTP(writer, req)
				want := "error"
				if mode == "ok" {
					want = "ok"
				}
				wantCount := 2
				if mode == "invalid" || mode == "usecase" {
					wantCount = 1
				}
				if len(p.stages) != wantCount {
					t.Fatalf("stages=%+v", p.stages)
				}
				for _, e := range p.stages {
					if e.scope != scope || e.result != want || e.duration <= 0 {
						t.Fatalf("stage=%+v want=%s", e, want)
					}
				}
				if wantCount == 2 && (p.stages[0].stage != "response" || p.stages[1].stage != "handler" || p.stages[1].duration < p.stages[0].duration) {
					t.Fatalf("stage boundaries=%+v", p.stages)
				}
				if mode == "restore" && rec.Code != 503 {
					t.Fatalf("restore status=%d", rec.Code)
				}
			})
		}
	}
}

type stageFailWriter struct{ *httptest.ResponseRecorder }

func (w stageFailWriter) Write([]byte) (int, error) { return 0, errors.New("broken connection") }
func TestConversationReadStagesDisabled(t *testing.T) {
	timer := (&Server{}).conversationReadTimer("list")
	if !timer.start().IsZero() {
		t.Fatal("disabled timer read clock")
	}
	if n := testing.AllocsPerRun(100, func() { timer.finish(nil, "handler", timer.start(), true) }); n != 0 {
		t.Fatalf("allocations=%v", n)
	}
}

func (p *readStageProbe) ConversationReadStageObservationEnabled() bool { return true }
