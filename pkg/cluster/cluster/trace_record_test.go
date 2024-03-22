package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetContextsWithRange(t *testing.T) {
	rd := newTraceRecord()

	rd.addProposeSpanRange(1, 10, nil, context.Background())
	rd.addProposeSpanRange(11, 20, nil, context.Background())
	rd.addProposeSpanRange(21, 40, nil, context.Background())

	ctxs := rd.getProposeContextsWithRange(1, 5)
	assert.Equal(t, 1, len(ctxs))

	ctxs = rd.getProposeContextsWithRange(6, 20)
	assert.Equal(t, 2, len(ctxs))

	ctxs = rd.getProposeContextsWithRange(30, 41)
	assert.Equal(t, 1, len(ctxs))

	ctxs = rd.getProposeContextsWithRange(1, 15)
	assert.Equal(t, 2, len(ctxs))

	ctxs = rd.getProposeContextsWithRange(1, 50)
	assert.Equal(t, 3, len(ctxs))

	ctxs = rd.getProposeContextsWithRange(50, 60)
	assert.Equal(t, 0, len(ctxs))

}
