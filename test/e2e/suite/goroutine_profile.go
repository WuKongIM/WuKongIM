//go:build e2e

package suite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// WriteGoroutineStacks saves at most 1 MiB from an enabled product debug API.
// The caller supplies a private output path and owns diagnostic failure policy;
// truncated stacks are diagnostic hints, never a proof of complete enumeration.
func WriteGoroutineStacks(ctx context.Context, apiBaseURL, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiBaseURL, "/")+"/debug/pprof/goroutine?debug=2", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("goroutine stacks: HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
