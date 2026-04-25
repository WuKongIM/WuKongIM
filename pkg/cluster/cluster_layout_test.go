package cluster

import (
	"reflect"
	"testing"
)

func TestClusterStructFieldBudget(t *testing.T) {
	if got, want := reflect.TypeOf(Cluster{}).NumField(), 15; got > want {
		t.Fatalf("Cluster top-level field count = %d, want <= %d", got, want)
	}
}
