package channelappend_test

import (
	"bytes"
	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPublicationClonesOwnMetadata(t *testing.T) {
	input := []byte("metadata")
	cmd := channelappend.SendCommand{PublicationMetadata: input}.Clone()
	item := channelappend.SendBatchItem{Command: channelappend.SendCommand{PublicationMetadata: input}}.Clone()
	msg := channelappend.Message{PublicationMetadata: input}.Clone()
	req := channelappend.AppendBatchRequest{Messages: []channelappend.Message{{PublicationMetadata: input}}}.Clone()
	res := channelappend.AppendBatchResult{Items: []channelappend.AppendBatchItemResult{{Message: channelappend.Message{PublicationMetadata: input}}}}.Clone()
	env := channelappend.CommittedEnvelope{PublicationMetadata: input}.Clone()
	outputs := [][]byte{cmd.PublicationMetadata, item.Command.PublicationMetadata, msg.PublicationMetadata, req.Messages[0].PublicationMetadata, res.Items[0].Message.PublicationMetadata, env.PublicationMetadata}
	clear(input)
	for i, b := range outputs {
		require.Equal(t, []byte("metadata"), b)
		clear(b)
		for _, other := range outputs[i+1:] {
			require.True(t, bytes.Equal(other, []byte("metadata")))
		}
	}
}
