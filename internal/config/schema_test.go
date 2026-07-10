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
