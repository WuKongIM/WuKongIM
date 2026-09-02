package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunPublishesLoopbackEndpointAndStopsOnCancellation(t *testing.T) {
	certificatePath, _ := writeTestCertificate(t, netip.MustParseAddr("198.51.100.20"), time.Now())
	listener := newBlockingListener("127.0.0.1:43127")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runWithListener(ctx, &stdout, &bytes.Buffer{}, []string{
			"-upstream", "https://198.51.100.20:19092",
			"-certificate", certificatePath,
		}, func(network, address string) (net.Listener, error) {
			if network != "tcp4" || address != "127.0.0.1:0" {
				t.Errorf("listen = %s %s", network, address)
			}
			return listener, nil
		})
	}()
	<-listener.accepted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runWithListener() error = %v", err)
	}
	if got, want := stdout.String(), "http://127.0.0.1:43127\n"; got != want {
		t.Fatalf("published endpoint = %q, want %q", got, want)
	}
}

func TestRunRejectsInvalidCommandInputBeforeListening(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown flag", args: []string{"-unknown"}, want: "flag provided but not defined"},
		{name: "positional argument", args: []string{"unexpected"}, want: "positional arguments are not supported"},
		{name: "invalid upstream", args: []string{"-upstream", "http://198.51.100.20:19092"}, want: "upstream must be an HTTPS Analysis origin"},
		{name: "missing certificate", args: []string{"-upstream", "https://198.51.100.20:19092"}, want: "certificate path is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := run(context.Background(), io.Discard, &stderr, test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRunReportsLoopbackAndServeFailures(t *testing.T) {
	certificatePath, _ := writeTestCertificate(t, netip.MustParseAddr("198.51.100.20"), time.Now())
	args := []string{
		"-upstream", "https://198.51.100.20:19092",
		"-certificate", certificatePath,
	}

	t.Run("listen", func(t *testing.T) {
		wantErr := errors.New("listen denied")
		err := runWithListener(context.Background(), io.Discard, io.Discard, args, func(string, string) (net.Listener, error) {
			return nil, wantErr
		})
		if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "listen on loopback") {
			t.Fatalf("runWithListener() error = %v", err)
		}
	})

	t.Run("serve", func(t *testing.T) {
		wantErr := errors.New("accept failed")
		listener := &acceptErrorListener{addr: testAddr("127.0.0.1:43128"), err: wantErr}
		var stdout bytes.Buffer
		err := runWithListener(context.Background(), &stdout, io.Discard, args, func(string, string) (net.Listener, error) {
			return listener, nil
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("runWithListener() error = %v, want wrapped %v", err, wantErr)
		}
		if got, want := stdout.String(), "http://127.0.0.1:43128\n"; got != want {
			t.Fatalf("published endpoint = %q, want %q", got, want)
		}
	})
}

func TestRunClosesListenerWhenEndpointCannotBePublished(t *testing.T) {
	certificatePath, _ := writeTestCertificate(t, netip.MustParseAddr("198.51.100.20"), time.Now())
	listener := newBlockingListener("127.0.0.1:43129")
	wantErr := errors.New("stdout closed")
	err := runWithListener(context.Background(), errorWriter{err: wantErr}, io.Discard, []string{
		"-upstream", "https://198.51.100.20:19092",
		"-certificate", certificatePath,
	}, func(string, string) (net.Listener, error) {
		return listener, nil
	})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "publish loopback endpoint") {
		t.Fatalf("runWithListener() error = %v", err)
	}
	select {
	case <-listener.closed:
	default:
		t.Fatal("listener remained open after endpoint publication failed")
	}
}

func TestReverseProxyPreservesMCPRequestAndStripsForwardingHeaders(t *testing.T) {
	upstream, err := url.Parse("https://198.51.100.20:19444/")
	if err != nil {
		t.Fatal(err)
	}
	handler := newReverseProxy(upstream, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got, want := request.URL.String(), "https://198.51.100.20:19444/mcp?session=abc"; got != want {
			t.Errorf("upstream URL = %q, want %q", got, want)
		}
		if got, want := request.Host, "198.51.100.20:19444"; got != want {
			t.Errorf("upstream Host = %q, want %q", got, want)
		}
		if got, want := request.Header.Get("Authorization"), "Bearer analysis-token"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		} else if got, want := string(body), `{"jsonrpc":"2.0","method":"tools/list"}`; got != want {
			t.Errorf("upstream body = %q, want %q", got, want)
		}
		for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
			if value := request.Header.Get(name); value != "" {
				t.Errorf("%s = %q, want empty", name, value)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","result":{}}`)),
		}, nil
	}), io.Discard)

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:43127/mcp?session=abc", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list"}`))
	request.Host = "attacker.invalid"
	request.Header.Set("Authorization", "Bearer analysis-token")
	request.Header.Set("Forwarded", "for=203.0.113.9")
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	request.Header.Set("X-Forwarded-Host", "attacker.invalid")
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Body.String(), `{"jsonrpc":"2.0","result":{}}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReverseProxyReturnsBoundedBadGateway(t *testing.T) {
	upstream, err := url.Parse("https://198.51.100.20:19092")
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	handler := newReverseProxy(upstream, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("secret upstream detail")
	}), &stderr)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://127.0.0.1:43127/mcp", strings.NewReader(`{}`)))
	if got, want := response.Code, http.StatusBadGateway; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Body.String(), "Analysis upstream unavailable\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if strings.Contains(response.Body.String(), "secret upstream detail") {
		t.Fatal("response leaked upstream failure detail")
	}
	if !strings.Contains(stderr.String(), "upstream request failed: secret upstream detail") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestPinnedCertificateVerifierEnforcesExactIdentity(t *testing.T) {
	now := time.Now().UTC()
	_, pinned := writeTestCertificate(t, netip.MustParseAddr("198.51.100.20"), now)
	upstream, err := url.Parse("https://198.51.100.20:19092")
	if err != nil {
		t.Fatal(err)
	}
	verify := newPinnedCertificateVerifier(upstream.Hostname(), pinned)

	if err := verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{pinned}}); err != nil {
		t.Fatalf("matching certificate rejected: %v", err)
	}

	_, other := writeTestCertificate(t, netip.MustParseAddr("198.51.100.20"), now)
	_, wrongSAN := writeTestCertificate(t, netip.MustParseAddr("203.0.113.8"), now)
	_, expired := writeTestCertificate(t, netip.MustParseAddr("198.51.100.20"), now.Add(-2*time.Hour))
	tests := []struct {
		name   string
		pinned *x509.Certificate
		state  tls.ConnectionState
		want   string
	}{
		{name: "missing peer", pinned: pinned, state: tls.ConnectionState{}, want: "fingerprint mismatch"},
		{name: "different certificate", pinned: pinned, state: tls.ConnectionState{PeerCertificates: []*x509.Certificate{other}}, want: "fingerprint mismatch"},
		{name: "wrong IP SAN", pinned: wrongSAN, state: tls.ConnectionState{PeerCertificates: []*x509.Certificate{wrongSAN}}, want: "not 198.51.100.20"},
		{name: "expired", pinned: expired, state: tls.ConnectionState{PeerCertificates: []*x509.Certificate{expired}}, want: "outside its validity window"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := newPinnedCertificateVerifier(upstream.Hostname(), test.pinned)(test.state)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verify() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestReadPinnedCertificateRequiresExactlyOneCertificate(t *testing.T) {
	validPath, want := writeTestCertificate(t, netip.MustParseAddr("198.51.100.20"), time.Now())
	validPEM, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid", func(t *testing.T) {
		got, err := readPinnedCertificate(validPath)
		if err != nil {
			t.Fatalf("readPinnedCertificate() error = %v", err)
		}
		if !got.Equal(want) {
			t.Fatal("parsed certificate does not match the pinned certificate")
		}
	})

	tests := []struct {
		name     string
		contents []byte
	}{
		{name: "not PEM", contents: []byte("not a certificate")},
		{name: "wrong PEM type", contents: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: want.Raw})},
		{name: "invalid DER", contents: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not DER")})},
		{name: "multiple certificates", contents: append(append([]byte(nil), validPEM...), validPEM...)},
		{name: "leading data", contents: append([]byte("unexpected\n"), validPEM...)},
		{name: "trailing data", contents: append(append([]byte(nil), validPEM...), []byte("unexpected")...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "analysis.pem")
			if err := os.WriteFile(path, test.contents, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readPinnedCertificate(path); err == nil {
				t.Fatal("readPinnedCertificate() error = nil")
			}
		})
	}
}

type blockingListener struct {
	addr      net.Addr
	accepted  chan struct{}
	closed    chan struct{}
	acceptOne sync.Once
	closeOne  sync.Once
}

type acceptErrorListener struct {
	addr net.Addr
	err  error
}

func (l *acceptErrorListener) Accept() (net.Conn, error) { return nil, l.err }
func (l *acceptErrorListener) Close() error              { return nil }
func (l *acceptErrorListener) Addr() net.Addr            { return l.addr }

func newBlockingListener(address string) *blockingListener {
	return &blockingListener{
		addr:     testAddr(address),
		accepted: make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	l.acceptOne.Do(func() { close(l.accepted) })
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingListener) Close() error {
	l.closeOne.Do(func() { close(l.closed) })
	return nil
}

func (l *blockingListener) Addr() net.Addr { return l.addr }

type testAddr string

func (a testAddr) Network() string { return "tcp" }
func (a testAddr) String() string  { return string(a) }

func writeTestCertificate(t *testing.T, address netip.Addr, now time.Time) (string, *x509.Certificate) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "analysis-test"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		IPAddresses:  []net.IP{net.IP(address.AsSlice())},
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "analysis.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, certificate
}
