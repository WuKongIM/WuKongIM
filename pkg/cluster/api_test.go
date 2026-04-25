package cluster

import "testing"

func TestClusterImplementsAPI(t *testing.T) {
	var api API = (*Cluster)(nil)
	if api == nil {
		t.Fatal("cluster API should not be nil")
	}
}
