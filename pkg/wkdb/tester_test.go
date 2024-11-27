package wkdb_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestTester(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	nw := time.Now()
	tester := wkdb.Tester{
		No:        "no",
		Addr:      "http://127.0.0.1:9466",
		CreatedAt: &nw,
		UpdatedAt: &nw,
	}

	t.Run("AddOrUpdateTester", func(t *testing.T) {
		err := d.AddOrUpdateTester(tester)
		assert.NoError(t, err)
	})

	t.Run("GetTesters", func(t *testing.T) {
		tt, err := d.GetTesters()
		assert.NoError(t, err)
		assert.NotEmpty(t, tt)
	})

	t.Run("GetTester", func(t *testing.T) {
		tt, err := d.GetTester(tester.No)
		assert.NoError(t, err)
		assert.Equal(t, tester.No, tt.No)
		assert.Equal(t, tester.Addr, tt.Addr)
		assert.NotEqual(t, 0, tt.Id)
		assert.Equal(t, tester.CreatedAt.Unix(), tt.CreatedAt.Unix())
		assert.Equal(t, tester.UpdatedAt.Unix(), tt.UpdatedAt.Unix())
	})

	t.Run("RemoveTester", func(t *testing.T) {
		err := d.RemoveTester(tester.No)
		assert.NoError(t, err)

		tt, err := d.GetTester(tester.No)
		assert.NoError(t, err)
		assert.Empty(t, tt)
	})

}
