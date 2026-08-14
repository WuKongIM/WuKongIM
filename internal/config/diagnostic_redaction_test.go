package config

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestRedactDiagnosticTOMLUsesSchemaForQuotedDottedAndMultilineValues(t *testing.T) {
	const input = `
"node"."id" = 1
"node"."data_dir" = "./data"
"cluster"."listen_addr" = "tcp://127.0.0.1:11110"
cluster.join_token = """join-token-canary
second-line-canary"""
manager.jwt_secret = 'jwt-secret-canary'
manager.users = [
  { username = "admin", password = "password-canary" },
]
bench.api_token = "api-token-canary"
api.external_ws_addr = "ws-address-canary"
prometheus.query_base_url = "prometheus-address-canary"
webhook.http_addr = "webhook-address-canary"
log.level = "info"
`

	redacted, err := RedactDiagnosticTOML([]byte(input))
	if err != nil {
		t.Fatalf("RedactDiagnosticTOML() error = %v", err)
	}
	for _, canary := range []string{
		"join-token-canary", "second-line-canary", "jwt-secret-canary",
		"password-canary", "api-token-canary", "ws-address-canary",
		"prometheus-address-canary", "webhook-address-canary",
	} {
		if strings.Contains(string(redacted), canary) {
			t.Fatalf("redacted TOML leaked %q:\n%s", canary, redacted)
		}
	}

	var decoded map[string]any
	if err := toml.Unmarshal(redacted, &decoded); err != nil {
		t.Fatalf("redacted output is not TOML: %v\n%s", err, redacted)
	}
	flat := map[string]any{}
	flattenTOML("", decoded, flat)
	for _, path := range []string{
		"cluster.join_token", "manager.jwt_secret", "bench.api_token",
		"api.external_ws_addr", "prometheus.query_base_url", "webhook.http_addr",
	} {
		if got := flat[path]; got != "******" {
			t.Fatalf("redacted %s = %#v, want masked string", path, got)
		}
	}
	users, ok := flat["manager.users"].([]any)
	if !ok || len(users) != 0 {
		t.Fatalf("redacted manager.users = %#v, want empty object list", flat["manager.users"])
	}
	if got := flat["log.level"]; got != "info" {
		t.Fatalf("non-sensitive log.level = %#v, want info", got)
	}
}

func TestRedactDiagnosticTOMLFailsClosedForMalformedOrUnknownInput(t *testing.T) {
	for _, tt := range []struct {
		name  string
		input string
	}{
		{name: "malformed", input: `cluster.join_token = "unterminated`},
		{name: "unknown", input: "[custom]\npassword = \"unknown-secret-canary\"\n"},
		{name: "unknown empty table", input: "[custom]\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			redacted, err := RedactDiagnosticTOML([]byte(tt.input))
			if err == nil {
				t.Fatalf("RedactDiagnosticTOML() error = nil, output = %q", redacted)
			}
			if len(redacted) != 0 {
				t.Fatalf("failed redaction returned partial output %q", redacted)
			}
		})
	}
}

func TestRedactDiagnosticTOMLDoesNotEchoMalformedSecretInError(t *testing.T) {
	const canary = "malformed-diagnostic-secret-canary"
	redacted, err := RedactDiagnosticTOML([]byte(`bench.api_token = "` + canary + "\n"))
	if err == nil {
		t.Fatalf("RedactDiagnosticTOML() error = nil, output = %q", redacted)
	}
	if len(redacted) != 0 {
		t.Fatalf("failed redaction returned partial output %q", redacted)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("redaction error leaked malformed secret: %v", err)
	}
	if got := err.Error(); got != "parse diagnostic config as TOML" {
		t.Fatalf("redaction syntax error = %q, want stable content-free message", got)
	}
}

func TestRedactDiagnosticTOMLRedactsEverySchemaDiagnosticSensitiveField(t *testing.T) {
	for _, field := range SchemaFields() {
		if !field.DiagnosticSensitive {
			continue
		}
		input := diagnosticRedactionInputForField(field)
		redacted, err := RedactDiagnosticTOML(input)
		if err != nil {
			t.Fatalf("RedactDiagnosticTOML(%s) error = %v\n%s", field.TOMLPath, err, input)
		}
		if strings.Contains(string(redacted), "diagnostic-sensitive-canary") {
			t.Fatalf("schema field %s leaked diagnostic canary:\n%s", field.TOMLPath, redacted)
		}
	}
}

func diagnosticRedactionInputForField(field SchemaField) []byte {
	value := `"diagnostic-sensitive-canary"`
	switch field.Kind {
	case string(kindStringList):
		value = `["diagnostic-sensitive-canary"]`
	case string(kindObjectList):
		value = `[{ value = "diagnostic-sensitive-canary" }]`
	}
	return []byte(field.TOMLPath + " = " + value + "\n")
}
