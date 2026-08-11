//go:build integration

package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedReverseProxyForwardsMCPAuthorization(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/mcp" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("upstream request = %s authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"jsonrpc":"2.0","result":{}}`)
	}))
	defer upstream.Close()
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(newPinnedReverseProxy(target, upstream.Certificate(), io.Discard))
	defer proxy.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, proxy.URL+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestReadPinnedCertificateRequiresOnePEMBlock(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	certificatePath := filepath.Join(t.TempDir(), "analysis.pem")
	contents := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(certificatePath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	certificate, err := readPinnedCertificate(certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !certificate.Equal(server.Certificate()) {
		t.Fatal("parsed certificate does not match")
	}
	if _, err := x509.ParseCertificate(certificate.Raw); err != nil {
		t.Fatal(err)
	}
}
