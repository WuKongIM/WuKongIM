package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestDiagnosticsTrackingRoutesPreserveTargetsAndBoundedEvidence(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC)
	expiresAt := createdAt.Add(15 * time.Minute)
	var createReq managementusecase.DiagnosticsTrackingCreateRequest
	var deletedRuleID string
	srv := New(Options{Management: managerNodesStub{
		diagnosticsTrackingCreateReqSink:  &createReq,
		diagnosticsTrackingDeleteRuleSink: &deletedRuleID,
		diagnosticsTrackingCreateResponse: managementusecase.DiagnosticsTrackingMutationResponse{
			Status: managementusecase.DiagnosticsTrackingStatusPartial,
			Rule: managementusecase.DiagnosticsTrackingRule{
				ID: "rule-channel", Target: "channel", ChannelKey: "room-a:2",
				ChannelID: "room-a", ChannelType: 2, SampleRate: 0.25,
				CreatedAt: createdAt, ExpiresAt: expiresAt,
			},
			Nodes: []managementusecase.DiagnosticsTrackingNodeResult{
				{NodeID: 2, Status: "ok"},
				{NodeID: 3, Status: "unavailable", Notes: []string{"runtime unavailable"}},
			},
			Notes: []string{"one target unavailable"},
		},
		diagnosticsTrackingListResponse: managementusecase.DiagnosticsTrackingListResponse{
			Status: managementusecase.DiagnosticsTrackingStatusOK,
			Rules: []managementusecase.DiagnosticsTrackingRule{
				{ID: "rule-channel", Target: "channel", ChannelID: "room-a", ChannelType: 2, SampleRate: 0.25},
			},
			Nodes: []managementusecase.DiagnosticsTrackingNodeResult{{NodeID: 2, Status: "ok"}},
		},
		diagnosticsTrackingDeleteResponse: managementusecase.DiagnosticsTrackingDeleteResponse{
			Status: managementusecase.DiagnosticsTrackingStatusOK,
			RuleID: "rule-channel",
			Nodes:  []managementusecase.DiagnosticsTrackingNodeResult{{NodeID: 2, Status: "ok"}},
		},
	}})

	create := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/manager/diagnostics/tracking-rules", strings.NewReader(`{
		"node_id":2,"target":" channel ","channel_id":" room-a ",
		"channel_type":2,"ttl_seconds":900,"sample_rate":0.25
	}`))
	createRequest.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(create, createRequest)
	if create.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", create.Code, create.Body.String())
	}
	if createReq.NodeID != 2 || createReq.Target != "channel" || createReq.ChannelID != "room-a" ||
		createReq.ChannelType != 2 || createReq.TTLSeconds != 900 || createReq.SampleRate != 0.25 {
		t.Fatalf("create request = %#v, want normalized exact channel target", createReq)
	}
	var createBody DiagnosticsTrackingMutationResponse
	if err := json.Unmarshal(create.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createBody.Status != "partial" || createBody.Rule.RuleID != "rule-channel" ||
		createBody.Rule.CreatedAt == nil || !createBody.Rule.CreatedAt.Equal(createdAt) ||
		createBody.Rule.ExpiresAt == nil || !createBody.Rule.ExpiresAt.Equal(expiresAt) ||
		len(createBody.Nodes) != 2 || len(createBody.Nodes[1].Notes) != 1 || len(createBody.Notes) != 1 {
		t.Fatalf("create body = %#v, want bounded partial evidence", createBody)
	}

	list := httptest.NewRecorder()
	srv.Engine().ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/manager/diagnostics/tracking-rules", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", list.Code, list.Body.String())
	}
	var listBody DiagnosticsTrackingListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listBody.Status != "ok" || len(listBody.Rules) != 1 || listBody.Rules[0].RuleID != "rule-channel" ||
		listBody.Rules[0].CreatedAt != nil || listBody.Rules[0].ExpiresAt != nil || len(listBody.Nodes) != 1 {
		t.Fatalf("list body = %#v, want stable list projection", listBody)
	}

	remove := httptest.NewRecorder()
	srv.Engine().ServeHTTP(remove, httptest.NewRequest(http.MethodDelete, "/manager/diagnostics/tracking-rules/rule-channel", nil))
	if remove.Code != http.StatusOK || deletedRuleID != "rule-channel" {
		t.Fatalf("delete status=%d rule=%q body=%s", remove.Code, deletedRuleID, remove.Body.String())
	}
	var deleteBody DiagnosticsTrackingDeleteResponse
	if err := json.Unmarshal(remove.Body.Bytes(), &deleteBody); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleteBody.Status != "ok" || deleteBody.RuleID != "rule-channel" || len(deleteBody.Nodes) != 1 {
		t.Fatalf("delete body = %#v, want exact rule result", deleteBody)
	}
}

func TestDiagnosticsTrackingCreateRejectsInvalidRuleBeforeManagement(t *testing.T) {
	var received managementusecase.DiagnosticsTrackingCreateRequest
	srv := New(Options{Management: managerNodesStub{diagnosticsTrackingCreateReqSink: &received}})
	cases := []string{
		`{"target":"sender_uid","ttl_seconds":60}`,
		`{"target":"channel","channel_id":"room-a","ttl_seconds":60}`,
		`{"target":"sender_uid","uid":"u1","ttl_seconds":0}`,
		`{"target":"sender_uid","uid":"u1","ttl_seconds":60,"sample_rate":1.1}`,
		`{"target":"unknown","uid":"u1","ttl_seconds":60}`,
		`{"target":`,
	}
	for _, body := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/manager/diagnostics/tracking-rules", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
	}
	if received.Target != "" {
		t.Fatalf("management called for invalid rule: %#v", received)
	}
}

func TestDiagnosticsTrackingErrorsFailClosed(t *testing.T) {
	invalid := New(Options{Management: managerNodesStub{
		diagnosticsTrackingCreateErr: diagnostics.ErrInvalidTrackingRule,
	}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/manager/diagnostics/tracking-rules", strings.NewReader(`{
		"target":"sender_uid","uid":"u1","ttl_seconds":60
	}`))
	request.Header.Set("Content-Type", "application/json")
	invalid.Engine().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid-rule status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	failed := New(Options{Management: managerNodesStub{
		diagnosticsTrackingListErr: errors.New("private node failure"),
	}})
	recorder = httptest.NewRecorder()
	failed.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/diagnostics/tracking-rules", nil))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "private node failure") {
		t.Fatalf("list failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	unwired := New(Options{})
	recorder = httptest.NewRecorder()
	unwired.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/manager/diagnostics/tracking-rules/rule-a", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
