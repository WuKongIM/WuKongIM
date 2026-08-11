// Command wkcloudanalysisbridge exposes one certificate-pinned Analysis endpoint on an ephemeral loopback HTTP port.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const shutdownTimeout = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "wkcloudanalysisbridge: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	flags := flag.NewFlagSet("wkcloudanalysisbridge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	upstreamValue := flags.String("upstream", "", "exact HTTPS Analysis origin")
	certificatePath := flags.String("certificate", "", "pinned Analysis certificate PEM")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("positional arguments are not supported")
	}
	upstream, err := validateUpstream(*upstreamValue)
	if err != nil {
		return err
	}
	pinned, err := readPinnedCertificate(*certificatePath)
	if err != nil {
		return err
	}
	handler := newPinnedReverseProxy(upstream, pinned, stderr)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()
	if _, err := fmt.Fprintf(stdout, "http://%s\n", listener.Addr()); err != nil {
		_ = listener.Close()
		return fmt.Errorf("publish loopback endpoint: %w", err)
	}
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

func validateUpstream(value string) (*url.URL, error) {
	upstream, err := url.Parse(strings.TrimSpace(value))
	if err != nil || upstream.Scheme != "https" || upstream.User != nil || upstream.RawQuery != "" || upstream.Fragment != "" ||
		(upstream.Path != "" && upstream.Path != "/") {
		return nil, errors.New("upstream must be an HTTPS Analysis origin")
	}
	address, err := netip.ParseAddr(upstream.Hostname())
	if err != nil || !address.Is4() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() {
		return nil, errors.New("upstream host must be one public IPv4 address")
	}
	if port := upstream.Port(); port != "19092" && port != "19444" {
		return nil, errors.New("upstream port must be an Analysis MCP port")
	}
	return upstream, nil
}

func readPinnedCertificate(path string) (*x509.Certificate, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("certificate path is required")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read certificate: %w", err)
	}
	block, rest := pem.Decode(contents)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("certificate file must contain exactly one PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	return certificate, nil
}

func newPinnedReverseProxy(upstream *url.URL, pinned *x509.Certificate, stderr io.Writer) http.Handler {
	wantFingerprint := sha256.Sum256(pinned.Raw)
	hostname := upstream.Hostname()
	transport := &http.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         hostname,
			InsecureSkipVerify: true, // Verification below uses the exact authenticated session certificate.
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.PeerCertificates) == 0 || sha256.Sum256(state.PeerCertificates[0].Raw) != wantFingerprint {
					return errors.New("upstream certificate fingerprint mismatch")
				}
				leaf := state.PeerCertificates[0]
				now := time.Now().UTC()
				if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
					return errors.New("upstream certificate is outside its validity window")
				}
				return leaf.VerifyHostname(hostname)
			},
		},
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.Out.Host = upstream.Host
			request.Out.Header.Del("Forwarded")
			request.Out.Header.Del("X-Forwarded-For")
			request.Out.Header.Del("X-Forwarded-Host")
			request.Out.Header.Del("X-Forwarded-Proto")
		},
		Transport: transport,
		ErrorLog:  log.New(stderr, "wkcloudanalysisbridge: ", 0),
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, err error) {
			log.New(stderr, "wkcloudanalysisbridge: ", 0).Printf("upstream request failed: %v", err)
			http.Error(response, "Analysis upstream unavailable", http.StatusBadGateway)
		},
	}
	return proxy
}
