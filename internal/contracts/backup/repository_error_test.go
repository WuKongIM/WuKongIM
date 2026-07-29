package backup

import (
	"errors"
	"strings"
	"testing"
)

func TestRepositoryAccessErrorIsSafeAndRetainsCause(t *testing.T) {
	cause := errors.New(
		"AccessKeyId=AKIA-SECRET SecretKey=secret-value " +
			"Authorization=credential X-Amz-Signature=signed-value",
	)
	accessErr := &RepositoryAccessError{
		Reason:       RepositoryAccessInvalidAccessKey,
		Stage:        RepositoryAccessWriteMarker,
		Provider:     StoreKindOSS,
		ProviderCode: "InvalidAccessKeyId",
		RequestID:    "request-1",
		NodeID:       2,
		Cause:        cause,
	}

	message := accessErr.Error()
	for _, secret := range []string{
		"AKIA-SECRET",
		"secret-value",
		"Authorization",
		"signed-value",
	} {
		if strings.Contains(message, secret) {
			t.Fatalf("safe error contains %q: %s", secret, message)
		}
	}
	for _, diagnostic := range []string{
		"invalid_access_key",
		"write_marker",
		"oss",
		"InvalidAccessKeyId",
		"request-1",
		"node=2",
	} {
		if !strings.Contains(message, diagnostic) {
			t.Fatalf("safe error omits %q: %s", diagnostic, message)
		}
	}
	if !errors.Is(accessErr, cause) {
		t.Fatal("RepositoryAccessError did not retain its cause")
	}
}
