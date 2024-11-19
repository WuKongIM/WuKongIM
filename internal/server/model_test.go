package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSubscriberGetRespMarshal(t *testing.T) {
	var resp subscriberGetResp
	resp = append(resp, "test1", "test2")
	data := resp.Marshal()

	var resp1 subscriberGetResp
	err := resp1.Unmarshal(data)
	assert.NoError(t, err)

	assert.Equal(t, len(resp), len(resp1))

}
