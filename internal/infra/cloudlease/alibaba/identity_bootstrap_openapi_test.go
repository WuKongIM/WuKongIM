package alibaba

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ims "github.com/alibabacloud-go/ims-20190815/v4/client"
)

func TestIdentityBootstrapOIDCReadDoesNotPanicInsideAlibabaSDK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"Code":"InternalError","Message":"test failure","RequestId":"test-request"}`))
	}))
	defer server.Close()

	config := (&openapiutil.Config{}).
		SetAccessKeyId("test-access-key").
		SetAccessKeySecret("test-access-secret").
		SetProtocol("http").
		SetEndpoint(strings.TrimPrefix(server.URL, "http://"))
	client, err := ims.NewClient(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &IdentityBootstrapOpenAPI{ims: client}

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("upsertIdentityOIDCProvider panicked in Alibaba SDK: %v", recovered)
		}
	}()
	if err := adapter.upsertIdentityOIDCProvider(context.Background(), IdentityOIDCProviderSpec{
		Name: "wukongim-cloud-lease-github",
	}); err == nil {
		t.Fatal("upsertIdentityOIDCProvider accepted the provider failure")
	}
}
