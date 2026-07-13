package cloudsim

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestRunLocatorRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	want := RunLocator{
		Schema:                 RunLocatorSchemaV1,
		RunID:                  "run-1",
		Provider:               "aliyun",
		Region:                 "cn-hangzhou",
		AccountIDHash:          "sha256:account",
		Repository:             "WuKongIM/WuKongIM",
		SourceSHA:              "0123456789012345678901234567890123456789",
		ScenarioDigest:         "sha256:scenario",
		CreatedAt:              createdAt,
		ExpiresAt:              createdAt.Add(2 * time.Hour),
		ProvisionWorkflowRunID: 12345,
	}
	var buf bytes.Buffer
	if err := EncodeRunLocator(&buf, want); err != nil {
		t.Fatalf("EncodeRunLocator() error = %v", err)
	}
	got, err := DecodeRunLocator(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("DecodeRunLocator() error = %v", err)
	}
	if got != want {
		t.Fatalf("locator = %#v, want %#v", got, want)
	}
}

func TestRunLocatorRejectsUnknownFieldsAndDiagnosticsPayload(t *testing.T) {
	_, err := DecodeRunLocator(bytes.NewBufferString(`{
		"schema":"wukongim.cloud_sim.run_locator/v1",
		"run_id":"run-1",
		"provider":"aliyun",
		"region":"cn-hangzhou",
		"account_id_hash":"sha256:account",
		"repository":"WuKongIM/WuKongIM",
		"source_sha":"0123456789012345678901234567890123456789",
		"scenario_digest":"sha256:scenario",
		"created_at":"2026-07-14T10:00:00Z",
		"expires_at":"2026-07-14T12:00:00Z",
		"provision_workflow_run_id":12345,
		"logs":["must-not-be-retained"]
	}`))
	if err == nil {
		t.Fatal("DecodeRunLocator() error = nil, want unknown-field rejection")
	}
}

func TestRunLocatorValidation(t *testing.T) {
	createdAt := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	locator := RunLocator{
		Schema: RunLocatorSchemaV1, RunID: "run-1", Provider: "aliyun", Region: "cn-hangzhou",
		AccountIDHash: "sha256:account", Repository: "WuKongIM/WuKongIM",
		SourceSHA: "0123456789012345678901234567890123456789", ScenarioDigest: "sha256:scenario",
		CreatedAt: createdAt, ExpiresAt: createdAt.Add(time.Hour), ProvisionWorkflowRunID: 1,
	}
	locator.ExpiresAt = locator.CreatedAt
	if err := locator.Validate(); !errors.Is(err, ErrInvalidRunLocator) {
		t.Fatalf("Validate() error = %v, want ErrInvalidRunLocator", err)
	}
}
