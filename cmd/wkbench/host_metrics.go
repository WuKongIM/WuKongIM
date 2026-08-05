package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var errHostMetricsConfig = errors.New("host metrics configuration failed")

type hostMetricsConfig struct {
	listen     string
	path       string
	mountpoint string
	device     string
}

type hostMetricsHandler struct {
	path       string
	mountpoint string
	device     string
}

func newHostMetricsCommand(stderr io.Writer) *cobra.Command {
	cfg := hostMetricsConfig{}
	cmd := &cobra.Command{
		Use:   "host-metrics",
		Short: "Expose native filesystem evidence for local chat-lifecycle shakeouts",
		RunE: func(_ *cobra.Command, _ []string) error {
			handler, err := newHostMetricsHandler(cfg)
			if err != nil || strings.TrimSpace(cfg.listen) == "" {
				return commandExit{code: exitConfig, message: "--listen, --path, --mountpoint, and --device are required"}
			}
			server := &http.Server{
				Addr: cfg.listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
			}
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return commandExit{code: exitInternal, message: "host metrics server failed"}
			}
			return nil
		},
	}
	cmd.SetOut(stderr)
	cmd.SetErr(stderr)
	cmd.Flags().StringVar(&cfg.listen, "listen", "127.0.0.1:19101", "private HTTP listen address")
	cmd.Flags().StringVar(&cfg.path, "path", "", "existing data directory whose filesystem is measured")
	cmd.Flags().StringVar(&cfg.mountpoint, "mountpoint", "", "declared mountpoint label expected by the lifecycle config")
	cmd.Flags().StringVar(&cfg.device, "device", "", "declared device label expected by the lifecycle config")
	return cmd
}

func newHostMetricsHandler(cfg hostMetricsConfig) (http.Handler, error) {
	if strings.TrimSpace(cfg.path) == "" || strings.TrimSpace(cfg.mountpoint) == "" || strings.TrimSpace(cfg.device) == "" ||
		strings.ContainsAny(cfg.mountpoint, "\r\n") || strings.ContainsAny(cfg.device, "\r\n") {
		return nil, errHostMetricsConfig
	}
	absolute, err := filepath.Abs(cfg.path)
	if err != nil {
		return nil, errHostMetricsConfig
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, errHostMetricsConfig
	}
	return &hostMetricsHandler{path: absolute, mountpoint: cfg.mountpoint, device: cfg.device}, nil
}

func (h *hostMetricsHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/healthz":
		writer.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(writer, "ok\n")
	case "/metrics":
		size, available, err := hostFilesystemBytes(h.path)
		if err != nil {
			http.Error(writer, "filesystem observation failed", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		labels := fmt.Sprintf("device=%s,mountpoint=%s", strconv.Quote(h.device), strconv.Quote(h.mountpoint))
		_, _ = fmt.Fprintf(writer, "node_filesystem_size_bytes{%s} %d\n", labels, size)
		_, _ = fmt.Fprintf(writer, "node_filesystem_avail_bytes{%s} %d\n", labels, available)
	default:
		http.NotFound(writer, request)
	}
}

func hostFilesystemBytes(path string) (int64, int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil || stat.Bsize <= 0 {
		return 0, 0, errHostMetricsConfig
	}
	sizeHigh, sizeLow := bits.Mul64(uint64(stat.Blocks), uint64(stat.Bsize))
	availableHigh, availableLow := bits.Mul64(uint64(stat.Bavail), uint64(stat.Bsize))
	if sizeHigh != 0 || availableHigh != 0 || sizeLow > math.MaxInt64 || availableLow > math.MaxInt64 || availableLow > sizeLow {
		return 0, 0, errHostMetricsConfig
	}
	return int64(sizeLow), int64(availableLow), nil
}
