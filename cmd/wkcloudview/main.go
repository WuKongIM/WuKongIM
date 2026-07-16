// Command wkcloudview serves the run-scoped public browser gateway for one
// Cloud Simulation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/WuKongIM/WuKongIM/internal/access/cloudview"
	"github.com/WuKongIM/WuKongIM/internal/app"
)

const maxConfigBytes = 256 << 10

type fileConfig struct {
	// ListenAddr is the public HTTP listener address.
	ListenAddr string `json:"listen_addr"`
	// Options contains the run-scoped Cloud View routing configuration.
	cloudview.Options
}

func main() { os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr)) }

func execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCommand(stdout, stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "wkcloudview",
		Short:         "Serve a Cloud Simulation public browser gateway",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	var validatePath string
	validate := &cobra.Command{
		Use:  "validate",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			configured, err := loadConfig(validatePath, os.Getenv)
			if err != nil {
				return err
			}
			if err := validateConfig(configured); err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, "valid")
			return err
		},
	}
	validate.Flags().StringVar(&validatePath, "config", "", "strict Cloud View JSON config")
	_ = validate.MarkFlagRequired("config")

	var servePath string
	serve := &cobra.Command{
		Use:  "serve",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			configured, err := loadConfig(servePath, os.Getenv)
			if err != nil {
				return err
			}
			return serveConfig(configured, stderr)
		},
	}
	serve.Flags().StringVar(&servePath, "config", "", "strict Cloud View JSON config")
	_ = serve.MarkFlagRequired("config")

	doctorOptions := doctorOptions{Username: "admin", Password: "a1234567", ExpectedTargets: 7}
	doctor := &cobra.Command{
		Use:  "doctor",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := runDoctor(cmd.Context(), doctorOptions)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(result)
		},
	}
	doctor.Flags().StringVar(&doctorOptions.BaseURL, "base-url", "", "public HTTP Cloud View origin")
	doctor.Flags().StringVar(&doctorOptions.Username, "username", doctorOptions.Username, "Manager username")
	doctor.Flags().StringVar(&doctorOptions.Password, "password", doctorOptions.Password, "Manager password")
	doctor.Flags().StringVar(&doctorOptions.GateToken, "gate-token", "", "provisioning gate probe token")
	doctor.Flags().IntVar(&doctorOptions.ExpectedTargets, "expected-targets", doctorOptions.ExpectedTargets, "required Prometheus active/up target count")
	_ = doctor.MarkFlagRequired("base-url")
	_ = doctor.MarkFlagRequired("gate-token")

	var statusURL, reportPath string
	annotate := &cobra.Command{
		Use:  "annotate-report",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return annotateReport(cmd.Context(), statusURL, reportPath)
		},
	}
	annotate.Flags().StringVar(&statusURL, "status-url", "", "simulator-local Cloud View status URL")
	annotate.Flags().StringVar(&reportPath, "report", "", "wkbench report JSON")
	_ = annotate.MarkFlagRequired("status-url")
	_ = annotate.MarkFlagRequired("report")

	root.AddCommand(validate, serve, doctor, annotate)
	return root
}

func loadConfig(path string, getenv func(string) string) (fileConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileConfig{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxConfigBytes+1))
	decoder.DisallowUnknownFields()
	var configured fileConfig
	if err := decoder.Decode(&configured); err != nil {
		return fileConfig{}, fmt.Errorf("decode cloud view config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fileConfig{}, errors.New("cloud view config contains trailing data")
	}
	if value := strings.TrimSpace(getenv("WK_CLOUD_VIEW_PUBLIC_BASE_URL")); value != "" {
		configured.PublicBaseURL = value
	}
	if value := strings.TrimSpace(getenv("WK_CLOUD_VIEW_GATE_PROBE_TOKEN")); value != "" {
		configured.GateProbeToken = value
	}
	return configured, nil
}

func validateConfig(configured fileConfig) error {
	if strings.TrimSpace(configured.ListenAddr) == "" || strings.TrimSpace(configured.RunID) == "" {
		return errors.New("listen_addr and run_id are required")
	}
	if _, _, err := net.SplitHostPort(configured.ListenAddr); err != nil {
		return fmt.Errorf("invalid listen_addr: %w", err)
	}
	// Validation must not create the configured runtime state files.
	options := configured.Options
	options.StatePath = ""
	options.MetricsPath = ""
	if _, err := app.NewCloudViewHandler(options); err != nil {
		return fmt.Errorf("invalid cloud view options: %w", err)
	}
	return nil
}

func serveConfig(configured fileConfig, stderr io.Writer) error {
	if err := validateConfig(configured); err != nil {
		return err
	}
	handler, err := app.NewCloudViewHandler(configured.Options)
	if err != nil {
		return fmt.Errorf("build cloud view: %w", err)
	}
	server := &http.Server{
		Addr:              configured.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	fmt.Fprintf(stderr, "wkcloudview: serving run %s on %s\n", configured.RunID, configured.ListenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
