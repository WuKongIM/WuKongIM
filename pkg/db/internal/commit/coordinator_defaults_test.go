package commit

import (
	"testing"
	"time"
)

func TestCoordinatorUsesMeasuredBatchingDefault(t *testing.T) {
	got := effectiveConfig(Config{}).FlushWindow
	want := 500 * time.Microsecond
	if got != want {
		t.Fatalf("default flush window = %s, want %s", got, want)
	}
}
