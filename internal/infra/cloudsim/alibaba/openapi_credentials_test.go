package alibaba

import "testing"

func TestNewOpenAPIFromEnvironmentAcceptsCredentialModes(t *testing.T) {
	tests := []struct {
		name            string
		accessKeyID     string
		accessKeySecret string
		securityToken   string
		wantError       bool
	}{
		{
			name:            "long-lived access key",
			accessKeyID:     "test-access-key-id",
			accessKeySecret: "test-access-key-secret",
		},
		{
			name:            "OIDC exchanged STS credential",
			accessKeyID:     "test-access-key-id",
			accessKeySecret: "test-access-key-secret",
			securityToken:   "test-security-token",
		},
		{name: "missing credentials", wantError: true},
		{name: "missing secret", accessKeyID: "test-access-key-id", wantError: true},
		{name: "missing id", accessKeySecret: "test-access-key-secret", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", test.accessKeyID)
			t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", test.accessKeySecret)
			t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", test.securityToken)

			_, err := NewOpenAPIFromEnvironment("cn-hangzhou")
			if test.wantError && err == nil {
				t.Fatal("NewOpenAPIFromEnvironment() error = nil, want error")
			}
			if !test.wantError && err != nil {
				t.Fatalf("NewOpenAPIFromEnvironment() error = %v", err)
			}
		})
	}
}

func TestNewOpenAPIFromEnvironmentRejectsMissingRegion(t *testing.T) {
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "test-access-key-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "test-access-key-secret")
	t.Setenv("ALIBABA_CLOUD_SECURITY_TOKEN", "")

	if _, err := NewOpenAPIFromEnvironment(""); err == nil {
		t.Fatal("NewOpenAPIFromEnvironment() error = nil, want error")
	}
}
