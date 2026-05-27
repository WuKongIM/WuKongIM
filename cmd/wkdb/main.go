package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
)

const (
	exitOK       = 0
	exitConfig   = 1
	exitQuery    = 2
	exitInternal = 3
)

func main() {
	os.Exit(runWithIO(os.Args[1:], os.Stdin, os.Stderr))
}

func runWithIO(args []string, stdin io.Reader, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkdb [flags] <query|repl>")
		return exitConfig
	}
	flags, rest, code := parseFlags(args, stderr)
	if code != exitOK {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "usage: wkdb [flags] <query|repl>")
		return exitConfig
	}
	switch rest[0] {
	case "query":
		if len(rest) < 2 {
			fmt.Fprintln(stderr, "usage: wkdb [flags] query <sql>")
			return exitConfig
		}
		return withStore(flags, stderr, func(store *inspect.Store, format string) int {
			return runQuery(context.Background(), store, format, strings.Join(rest[1:], " "), os.Stdout, stderr)
		})
	case "repl":
		return withStore(flags, stderr, func(store *inspect.Store, format string) int {
			return runREPL(context.Background(), store, format, stdin, os.Stdout, stderr)
		})
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", rest[0])
		return exitConfig
	}
}

func parseFlags(args []string, stderr io.Writer) (cliFlags, []string, int) {
	var flags cliFlags
	var hashSlotCount uint
	fs := flag.NewFlagSet("wkdb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&flags.configPath, "config", "", "path to wukongim.conf")
	fs.StringVar(&flags.dataDir, "data-dir", "", "node data directory")
	fs.StringVar(&flags.metaPath, "meta-path", "", "metadata store path")
	fs.StringVar(&flags.messagePath, "message-path", "", "message store path")
	fs.UintVar(&hashSlotCount, "hash-slot-count", 0, "cluster hash slot count")
	fs.StringVar(&flags.format, "format", "table", "output format: table, json, jsonl")
	if err := fs.Parse(args); err != nil {
		return cliFlags{}, nil, exitConfig
	}
	if hashSlotCount > 65535 {
		fmt.Fprintln(stderr, "--hash-slot-count must be <= 65535")
		return cliFlags{}, nil, exitConfig
	}
	flags.hashSlotCount = uint16(hashSlotCount)
	return flags, fs.Args(), exitOK
}

func withStore(flags cliFlags, stderr io.Writer, fn func(*inspect.Store, string) int) int {
	cfg, err := resolveCLIConfig(flags, os.Environ())
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return exitConfig
	}
	store, err := inspect.OpenStore(cfg.options)
	if err != nil {
		fmt.Fprintf(stderr, "open store: %v\n", err)
		return exitConfig
	}
	defer store.Close()
	return fn(store, cfg.format)
}

func runQuery(ctx context.Context, store *inspect.Store, format string, sql string, stdout, stderr io.Writer) int {
	result, err := store.Query(ctx, sql)
	if err != nil {
		fmt.Fprintf(stderr, "query error: %v\n", err)
		return exitQuery
	}
	if err := renderResult(stdout, format, result); err != nil {
		fmt.Fprintf(stderr, "render error: %v\n", err)
		return exitInternal
	}
	return exitOK
}

func runREPL(ctx context.Context, store *inspect.Store, format string, stdin io.Reader, stdout, stderr io.Writer) int {
	scanner := bufio.NewScanner(stdin)
	exitCode := exitOK
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return exitCode
		}
		if code := runQuery(ctx, store, format, line, stdout, stderr); code != exitOK && exitCode == exitOK {
			exitCode = code
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "read repl: %v\n", err)
		return exitInternal
	}
	return exitCode
}
