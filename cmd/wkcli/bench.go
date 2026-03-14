package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// benchConfig holds all benchmark parameters.
type benchConfig struct {
	Servers    []string      // HTTP server addresses (for /route discovery)
	Addrs      []string      // Discovered TCP addresses (tcp://ip:port)
	Pairs      int           // Number of sender/receiver pairs
	Duration   time.Duration // Benchmark duration
	PayloadLen int           // Message payload size in bytes
	Rate       int           // Per-pair send rate (msgs/s), 0 = unlimited
	Warmup     time.Duration // Warmup duration
	NoEncrypt  bool          // Disable encryption
	NoPersist  bool          // Skip persistence
	Output     string        // Output format: text, json
}

func (c *benchConfig) isCluster() bool { return len(c.Addrs) > 1 }

func (c *benchConfig) mode() string {
	if c.isCluster() {
		return "Cluster"
	}
	return "Single"
}

func (c *benchConfig) validate() error {
	if c.Pairs < 1 {
		return fmt.Errorf("--pairs must be >= 1")
	}
	if c.Duration < time.Second {
		return fmt.Errorf("--duration must be >= 1s")
	}
	if c.PayloadLen < 16 {
		return fmt.Errorf("--payload must be >= 16")
	}
	if c.Rate < 0 {
		return fmt.Errorf("--rate must be >= 0")
	}
	if c.Output != "text" && c.Output != "json" {
		return fmt.Errorf("--output must be text or json")
	}
	if len(c.Addrs) == 0 {
		return fmt.Errorf("no TCP addresses discovered; check server connectivity")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(benchCmd)

	benchCmd.Flags().Int("pairs", 10, "Number of sender/receiver pairs")
	benchCmd.Flags().String("duration", "30s", "Benchmark duration")
	benchCmd.Flags().Int("payload", 256, "Message payload size in bytes (min 16)")
	benchCmd.Flags().Int("rate", 0, "Per-pair send rate (msgs/s), 0 = unlimited")
	benchCmd.Flags().String("warmup", "3s", "Warmup duration")
	benchCmd.Flags().Bool("no-encrypt", false, "Disable message encryption")
	benchCmd.Flags().Bool("no-persist", true, "Skip message persistence")
	benchCmd.Flags().String("output", "text", "Output format: text, json")
}

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Run benchmark against WuKongIM server",
	Long: `Run a benchmark to measure message throughput and end-to-end latency.

Examples:
  # Single node (uses configured context server)
  wkcli bench --pairs 5 --duration 10s

  # Cluster mode (auto-discover TCP addresses via /route)
  wkcli bench --server http://node1:5001,http://node2:5001 --pairs 10 --duration 30s`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pairs, _ := cmd.Flags().GetInt("pairs")
		durationStr, _ := cmd.Flags().GetString("duration")
		payloadLen, _ := cmd.Flags().GetInt("payload")
		rate, _ := cmd.Flags().GetInt("rate")
		warmupStr, _ := cmd.Flags().GetString("warmup")
		noEncrypt, _ := cmd.Flags().GetBool("no-encrypt")
		noPersist, _ := cmd.Flags().GetBool("no-persist")
		output, _ := cmd.Flags().GetString("output")

		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return fmt.Errorf("invalid --duration: %w", err)
		}
		warmup, err := time.ParseDuration(warmupStr)
		if err != nil {
			return fmt.Errorf("invalid --warmup: %w", err)
		}

		// Determine HTTP servers for /route discovery.
		var servers []string
		serverFlag := getServerURL()
		if serverFlag != "" {
			for _, s := range strings.Split(serverFlag, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					servers = append(servers, s)
				}
			}
		}
		if len(servers) == 0 {
			return fmt.Errorf("server not configured. Run: wkcli context set --server <url>")
		}

		// Discover TCP addresses via /route.
		addrs, err := discoverTCPAddrs(servers)
		if err != nil {
			return err
		}

		cfg := &benchConfig{
			Servers:    servers,
			Addrs:      addrs,
			Pairs:      pairs,
			Duration:   duration,
			PayloadLen: payloadLen,
			Rate:       rate,
			Warmup:     warmup,
			NoEncrypt:  noEncrypt,
			NoPersist:  noPersist,
			Output:     output,
		}
		if err := cfg.validate(); err != nil {
			return err
		}

		// Print banner.
		printBenchBanner(cfg)

		// Run benchmark.
		return runBench(cfg)
	},
}

// discoverTCPAddrs calls GET /route on each HTTP server to get tcp_addr.
func discoverTCPAddrs(servers []string) ([]string, error) {
	var addrs []string
	for _, server := range servers {
		url := strings.TrimRight(server, "/") + "/route"
		resp, err := http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("discover %s: %w", server, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response from %s: %w", server, err)
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("discover %s: status %d: %s", server, resp.StatusCode, string(body))
		}
		var result struct {
			TCPAddr string `json:"tcp_addr"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response from %s: %w", server, err)
		}
		if result.TCPAddr == "" {
			return nil, fmt.Errorf("discover %s: empty tcp_addr", server)
		}
		addrs = append(addrs, "tcp://"+result.TCPAddr)
	}
	return addrs, nil
}

// printBenchBanner prints the benchmark configuration summary.
func printBenchBanner(cfg *benchConfig) {
	fmt.Println()
	fmt.Printf("  %sWuKongIM Benchmark%s\n", colorBold, colorReset)
	fmt.Printf("  %s──────────────────────────────────────%s\n", colorGray, colorReset)
	printInfo("Mode", cfg.mode())
	for i, addr := range cfg.Addrs {
		if i == 0 {
			printInfo("Address", addr)
		} else {
			printInfo("", addr)
		}
	}
	printInfo("Pairs", fmt.Sprintf("%d", cfg.Pairs))
	printInfo("Payload", fmt.Sprintf("%d bytes", cfg.PayloadLen))
	printInfo("Duration", cfg.Duration.String())
	if cfg.Rate > 0 {
		printInfo("Rate", fmt.Sprintf("%d msgs/s per pair", cfg.Rate))
	} else {
		printInfo("Rate", "unlimited")
	}
	printInfo("Encrypt", fmt.Sprintf("%v", !cfg.NoEncrypt))
	printInfo("Persist", fmt.Sprintf("%v", !cfg.NoPersist))
	fmt.Println()
}
