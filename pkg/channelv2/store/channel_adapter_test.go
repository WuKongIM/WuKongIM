package store

import "testing"

func TestOldStoreAdapterContract(t *testing.T) {
	factory := NewOldStoreFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	testStoreContract(t, factory)
}
