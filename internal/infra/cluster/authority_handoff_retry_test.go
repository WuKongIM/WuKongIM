package cluster

import (
	"testing"
	"time"
)

func TestBoundedAuthorityHandoffBackoff(t *testing.T) {
	tests := []struct {
		name    string
		base    time.Duration
		max     time.Duration
		attempt int
		want    time.Duration
	}{
		{name: "first", base: 5 * time.Millisecond, max: 100 * time.Millisecond, attempt: 1, want: 5 * time.Millisecond},
		{name: "growth", base: 5 * time.Millisecond, max: 100 * time.Millisecond, attempt: 4, want: 40 * time.Millisecond},
		{name: "cap", base: 5 * time.Millisecond, max: 100 * time.Millisecond, attempt: 8, want: 100 * time.Millisecond},
		{name: "disabled", base: 0, max: 100 * time.Millisecond, attempt: 1, want: 0},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := boundedAuthorityHandoffBackoff(testCase.base, testCase.max, testCase.attempt); got != testCase.want {
				t.Fatalf("boundedAuthorityHandoffBackoff() = %s, want %s", got, testCase.want)
			}
		})
	}
}
