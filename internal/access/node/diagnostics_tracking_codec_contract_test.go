package node

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
)

func TestDiagnosticsTrackingCodecPreservesRuleLifecycleFields(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 10, 0, 0, 125, time.UTC)
	request := diagnosticsTrackingRequest{
		Op: diagnosticsTrackingOpAdd,
		Rule: diagnostics.TrackingRuleInput{
			ID:         "rule-sender",
			Target:     diagnostics.TrackingTargetSenderUID,
			UID:        "user-1",
			ChannelKey: "room-1:2",
			TTL:        90 * time.Second,
			SampleRate: 0.375,
		},
		RuleID: "rule-old",
	}
	body, err := encodeDiagnosticsTrackingRequest(request)
	if err != nil {
		t.Fatalf("encodeDiagnosticsTrackingRequest() error = %v", err)
	}
	decodedRequest, err := decodeDiagnosticsTrackingRequest(body)
	if err != nil {
		t.Fatalf("decodeDiagnosticsTrackingRequest() error = %v", err)
	}
	if !reflect.DeepEqual(decodedRequest, request) {
		t.Fatalf("tracking request round trip = %#v, want %#v", decodedRequest, request)
	}

	rule := diagnostics.TrackingRule{
		ID:         "rule-sender",
		Target:     diagnostics.TrackingTargetSenderUID,
		UID:        "user-1",
		ChannelKey: "room-1:2",
		SampleRate: 0.375,
		CreatedAt:  createdAt,
		ExpiresAt:  createdAt.Add(90 * time.Second),
	}
	response := diagnosticsTrackingResponse{
		Status: rpcStatusOK,
		Rule:   rule,
		Rules: []diagnostics.TrackingRule{
			rule,
			{ID: "rule-channel", Target: diagnostics.TrackingTargetChannel, ChannelKey: "room-2:2", SampleRate: 1, CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Minute)},
		},
	}
	body, err = encodeDiagnosticsTrackingResponse(response)
	if err != nil {
		t.Fatalf("encodeDiagnosticsTrackingResponse() error = %v", err)
	}
	decodedResponse, err := decodeDiagnosticsTrackingResponse(body)
	if err != nil {
		t.Fatalf("decodeDiagnosticsTrackingResponse() error = %v", err)
	}
	if !reflect.DeepEqual(decodedResponse, response) {
		t.Fatalf("tracking response round trip = %#v, want %#v", decodedResponse, response)
	}
}

func TestDiagnosticsTrackingCodecRejectsUnboundedOrAmbiguousFrames(t *testing.T) {
	validRequest, err := encodeDiagnosticsTrackingRequest(diagnosticsTrackingRequest{
		Op: diagnosticsTrackingOpDelete, RuleID: "rule-1",
	})
	if err != nil {
		t.Fatalf("encode request error = %v", err)
	}
	validResponse, err := encodeDiagnosticsTrackingResponse(diagnosticsTrackingResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encode response error = %v", err)
	}

	requestCases := []struct {
		name string
		body []byte
	}{
		{name: "oversized", body: make([]byte, maxDiagnosticsBodyBytes+1)},
		{name: "wrong magic", body: append([]byte("WKDTX1"), validRequest[len(diagnosticsTrackingRequestMagic):]...)},
		{name: "truncated", body: validRequest[:len(validRequest)-1]},
		{name: "trailing", body: append(append([]byte(nil), validRequest...), 0)},
	}
	for _, test := range requestCases {
		t.Run("request "+test.name, func(t *testing.T) {
			if _, err := decodeDiagnosticsTrackingRequest(test.body); err == nil {
				t.Fatal("decodeDiagnosticsTrackingRequest() error = nil")
			}
		})
	}

	tooManyRules := append([]byte(nil), diagnosticsTrackingResponseMagic[:]...)
	tooManyRules = appendString(tooManyRules, rpcStatusOK)
	tooManyRules = appendString(tooManyRules, "")
	tooManyRules = appendDiagnosticsTrackingRule(tooManyRules, diagnostics.TrackingRule{})
	tooManyRules = appendUvarint(tooManyRules, maxDiagnosticsTrackingRules+1)
	responseCases := []struct {
		name string
		body []byte
	}{
		{name: "oversized", body: []byte(strings.Repeat("x", maxDiagnosticsBodyBytes+1))},
		{name: "wrong magic", body: append([]byte("WKDTX1"), validResponse[len(diagnosticsTrackingResponseMagic):]...)},
		{name: "truncated", body: validResponse[:len(validResponse)-1]},
		{name: "trailing", body: append(append([]byte(nil), validResponse...), 0)},
		{name: "rule count", body: tooManyRules},
	}
	for _, test := range responseCases {
		t.Run("response "+test.name, func(t *testing.T) {
			if _, err := decodeDiagnosticsTrackingResponse(test.body); err == nil {
				t.Fatal("decodeDiagnosticsTrackingResponse() error = nil")
			}
		})
	}
}
