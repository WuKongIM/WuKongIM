package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithStderr(args, os.Stderr)
}

func runWithStderr(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench <run|worker|validate|doctor|report>")
		return 1
	}
	switch args[0] {
	case "worker":
		return runWorker(args[1:], stderr)
	case "validate", "doctor", "run", "report":
		fmt.Fprintf(stderr, "%s is not implemented yet\n", args[0])
		return 6
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 1
	}
}

type workerCLIConfig struct {
	listen string
	server worker.Config
}

func runWorker(args []string, stderr io.Writer) int {
	cfg, code := parseWorkerConfig(args, stderr)
	if code != 0 {
		return code
	}
	if err := http.ListenAndServe(cfg.listen, worker.NewServer(cfg.server)); err != nil {
		fmt.Fprintf(stderr, "worker server failed: %v\n", err)
		return 1
	}
	return 0
}

func parseWorkerConfig(args []string, stderr io.Writer) (workerCLIConfig, int) {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := workerCLIConfig{listen: "127.0.0.1:19090"}
	fs.StringVar(&cfg.listen, "listen", cfg.listen, "worker control listen address")
	fs.StringVar(&cfg.server.WorkDir, "work-dir", "", "directory for worker control state")
	fs.StringVar(&cfg.server.ControlToken, "control-token", os.Getenv("WK_BENCH_WORKER_TOKEN"), "bearer token for worker control API")
	fs.BoolVar(&cfg.server.InsecureControl, "insecure-control", false, "allow unauthenticated worker control API")
	if err := fs.Parse(args); err != nil {
		return workerCLIConfig{}, 1
	}
	if cfg.server.ControlToken == "" && !cfg.server.InsecureControl {
		fmt.Fprintln(stderr, "--control-token is required unless --insecure-control=true")
		return workerCLIConfig{}, 1
	}
	return cfg, 0
}
