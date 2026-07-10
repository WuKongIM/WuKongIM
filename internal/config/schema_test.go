package config

import "testing"

func TestSchemaFieldsExposeDiagnosticSensitivity(t *testing.T) {
	fields := make(map[string]SchemaField)
	for _, field := range SchemaFields() {
		fields[field.TOMLPath] = field
		if field.Sensitive && !field.DiagnosticSensitive {
			t.Fatalf("field %s is startup-sensitive but not diagnostic-sensitive", field.TOMLPath)
		}
	}

	tests := []struct {
		path                string
		sensitive           bool
		diagnosticSensitive bool
	}{
		{path: "node.id"},
		{path: "manager.users", sensitive: true, diagnosticSensitive: true},
		{path: "api.external_ws_addr", diagnosticSensitive: true},
		{path: "api.external_wss_addr", diagnosticSensitive: true},
		{path: "prometheus.query_base_url", diagnosticSensitive: true},
		{path: "webhook.http_addr", diagnosticSensitive: true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			field, ok := fields[tt.path]
			if !ok {
				t.Fatalf("SchemaFields() missing %s", tt.path)
			}
			if field.Sensitive != tt.sensitive {
				t.Fatalf("SchemaFields()[%s].Sensitive = %v, want %v", tt.path, field.Sensitive, tt.sensitive)
			}
			if field.DiagnosticSensitive != tt.diagnosticSensitive {
				t.Fatalf("SchemaFields()[%s].DiagnosticSensitive = %v, want %v", tt.path, field.DiagnosticSensitive, tt.diagnosticSensitive)
			}
		})
	}
}

func TestSchemaFieldsExposePresenceTouchMaxRoutesPerFlush(t *testing.T) {
	for _, field := range SchemaFields() {
		if field.TOMLPath != "presence.touch_max_routes_per_flush" {
			continue
		}
		if field.EnvKey != "WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH" {
			t.Fatalf("EnvKey = %q, want WK_PRESENCE_TOUCH_MAX_ROUTES_PER_FLUSH", field.EnvKey)
		}
		if field.Kind != "int" {
			t.Fatalf("Kind = %q, want int", field.Kind)
		}
		return
	}
	t.Fatal("SchemaFields() missing presence.touch_max_routes_per_flush")
}
