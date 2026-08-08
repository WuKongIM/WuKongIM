package alibaba

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ims "github.com/alibabacloud-go/ims-20190815/v4/client"
)

func TestCloudLeaseOIDCFingerprintsIncludePresentedCAChain(t *testing.T) {
	leaf := &x509.Certificate{Raw: []byte("leaf")}
	intermediate := &x509.Certificate{Raw: []byte("intermediate")}
	presentedRoot := &x509.Certificate{Raw: []byte("presented-root")}
	trustedRoot := &x509.Certificate{Raw: []byte("trusted-root")}

	got, err := cloudLeaseOIDCFingerprintsFromTLSState(tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf, intermediate, presentedRoot},
		VerifiedChains:   [][]*x509.Certificate{{leaf, intermediate, presentedRoot, trustedRoot}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		certificateSHA1Fingerprint(intermediate),
		certificateSHA1Fingerprint(presentedRoot),
		certificateSHA1Fingerprint(trustedRoot),
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fingerprints = %#v, want presented CA chain %#v", got, want)
	}
}

func certificateSHA1Fingerprint(certificate *x509.Certificate) string {
	digest := sha1.Sum(certificate.Raw) // #nosec G505 -- matches Alibaba RAM's OIDC contract.
	return hex.EncodeToString(digest[:])
}

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
