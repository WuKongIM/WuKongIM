package wkdb_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddAndGetSystemUids(t *testing.T) {

	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uids := []string{"test1", "test2", "test3"}

	err = d.AddSystemUids(uids)
	assert.NoError(t, err)

	resultUids, err := d.GetSystemUids()
	assert.NoError(t, err)

	assert.ElementsMatch(t, uids, resultUids)
}
