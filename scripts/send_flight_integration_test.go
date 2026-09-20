//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real shell owner after a test-process failure, without building
// a server. A captured tmpfs trace must survive even when Go cleanups never ran.
func Test500QPSSeamPreservesVerdictAndRetainsInterruptedFlight(t *testing.T) {
	for _, tc := range []struct {
		name, benchmark, analysis string
		pass                      bool
	}{
		{"failed_workload", "42", "0", false},
		{"timeout_after_capture", "124", "0", false},
		{"failed_analysis", "0", "1", true},
		{"failed_retention", "0", "0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			stubs := map[string]string{
				"uname": `echo x86_64`, "nproc": `echo 4`, "lscpu": `echo '{}'`,
				"findmnt":   `if [[ "$1" == -n ]]; then echo tmpfs; else echo '{}'; fi`,
				"df":        "printf 'Filesystem blocks used available capacity mount\\nfixture 100 1 99 1%% /\\n'",
				"sha256sum": `echo 'fixture-hash benchmark.test'`,
				"timeout":   `shift; exec "$@"`,
				"mktemp":    `exec "$WK_REAL_MKTEMP" -d "$WK_FAKE_SHM/wk-send-flight.XXXXXXXX"`,
				"go": `case "$1 ${2:-}" in
 'env GOVERSION') echo go1.25.11 ;;
 'version '*) echo go1.25.11 ;;
 'test -c')
   cp "$WK_FAKE_BENCHMARK" "${@: -1}"
   chmod 700 "${@: -1}"
   ;;
 'tool trace'|'tool pprof') echo diagnostic; exit "$WK_FAKE_ANALYSIS_STATUS" ;;
 *) exit 99 ;;
esac`,
			}
			for name, body := range stubs {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/usr/bin/env bash\nset -eu\n"+body+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			fake := filepath.Join(root, "fake-benchmark")
			body := `#!/usr/bin/env bash
set -eu
echo run >>"$WK_FAKE_CALLS"
mkdir -p "$WK_BENCH_SEND_FLIGHT_DIR"
echo '{"status":"captured","trace_complete":true,"handlers_completed":false}' >"$WK_BENCH_SEND_FLIGHT_DIR/window.json"
echo fixture-trace >"$WK_BENCH_SEND_FLIGHT_DIR/execution.trace"
if [[ "$WK_FAKE_NAME" == failed_retention ]]; then
  # Occupy the destination with a file, so mkdir and all status writes fail.
  echo blocked >"${WK_BENCH_ARRIVAL_REPORT%/*}/flight"
fi
echo '{}' >"$WK_BENCH_ARRIVAL_REPORT"
exit "$WK_FAKE_BENCHMARK_STATUS"
`
			if err := os.WriteFile(fake, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			mktemp, err := exec.LookPath("mktemp")
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(root, "evidence")
			cmd := exec.Command("bash", "run-500qps-seam.sh", "mixed-send", out)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"WK_FAKE_NAME="+tc.name, "WK_REAL_MKTEMP="+mktemp, "WK_FAKE_SHM="+root, "WK_FAKE_BENCHMARK="+fake,
				"WK_FAKE_CALLS="+filepath.Join(root, "calls"), "WK_FAKE_BENCHMARK_STATUS="+tc.benchmark, "WK_FAKE_ANALYSIS_STATUS="+tc.analysis)
			log, err := cmd.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("wrong original verdict: %v %s", err, log)
			}
			data, err := os.ReadFile(filepath.Join(out, "result.json"))
			want := `"status":"failed"`
			if tc.pass {
				want = `"status":"pass"`
			}
			if err != nil || !strings.Contains(string(data), want) {
				t.Fatalf("lost verdict: %s %v", data, err)
			}
			if tc.name == "failed_retention" {
				if !strings.Contains(string(log), "rolling SEND trace retention incomplete") {
					t.Fatal("missing log fallback")
				}
			} else {
				for _, name := range []string{"window.json", "execution.trace", "analysis-status.txt"} {
					if _, err := os.Stat(filepath.Join(out, "flight", name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			calls, err := os.ReadFile(filepath.Join(root, "calls"))
			if err != nil || string(calls) != "run\n" {
				t.Fatalf("retried fixture: %q %v", calls, err)
			}
			stages, err := filepath.Glob(filepath.Join(root, "wk-send-flight.*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("leaked staging: %v %v", stages, err)
			}
		})
	}
}
