package main

import "testing"

func TestValidateUpstreamAcceptsOnlyPublicAnalysisOrigins(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{value: "https://198.51.100.20:19092", valid: true},
		{value: "https://203.0.113.8:19444/", valid: true},
		{value: "http://198.51.100.20:19444", valid: false},
		{value: "https://analysis.example.com:19444", valid: false},
		{value: "https://10.42.0.10:19444", valid: false},
		{value: "https://127.0.0.1:19444", valid: false},
		{value: "https://198.51.100.20:443", valid: false},
		{value: "https://198.51.100.20:19444/mcp", valid: false},
		{value: "https://user@198.51.100.20:19444", valid: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			_, err := validateUpstream(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("validateUpstream(%q) error = %v, valid = %v", test.value, err, test.valid)
			}
		})
	}
}
