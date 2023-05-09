package server

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/limutil"
	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

const RunTimes = 1e6

func connectTest(t *testing.T) (*TestConn, *Server) {

	var clientPubKey [32]byte
	_, clientPubKey = limutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对

	s := New(NewTestOptions())
	data, err := s.opts.Proto.EncodeFrame(&lmproto.ConnectPacket{
		Version:         lmproto.LatestVersion,
		ClientKey:       base64.StdEncoding.EncodeToString(clientPubKey[:]),
		ClientTimestamp: time.Now().Unix(),
		UID:             "test",
		Token:           "",
	}, 0)
	assert.NoError(t, err)

	c := NewTestConn()

	s.OnPacket(c, data)

	result := <-c.WriteChan()

	frame, _, err := s.opts.Proto.DecodeFrame(result, lmproto.LatestVersion)

	assert.NoError(t, err)
	assert.Equal(t, frame.(*lmproto.ConnackPacket).ReasonCode, lmproto.ReasonSuccess)

	return c, s
}

func TestHandleConnect(t *testing.T) {
	connectTest(t)
}

func TestHandleSend(t *testing.T) {
	c := NewTestConn()
	c.SetAuthed(true)
	s := New(NewTestOptions(zapcore.DebugLevel))

	data, err := s.opts.Proto.EncodeFrame(&lmproto.SendPacket{
		ClientMsgNo: "123",
		ClientSeq:   1,
		ChannelID:   "test",
		ChannelType: 1,
		Payload:     []byte("hello"),
	}, 0)
	assert.NoError(t, err)
	s.OnPacket(c, data)

	result := <-c.WriteChan()
	frame, _, err := s.opts.Proto.DecodeFrame(result, lmproto.LatestVersion)
	assert.NoError(t, err)

	assert.Equal(t, frame.(*lmproto.SendackPacket).ReasonCode, lmproto.ReasonSuccess)

}

func BenchmarkHandleSend(b *testing.B) {

	opts := NewTestOptions(zapcore.InfoLevel)
	s := New(opts)

	for i := 0; i < b.N; i++ {
		for j := 0; j < RunTimes; j++ {
			c := NewTestConn()
			c.SetAuthed(true)
			data, err := s.opts.Proto.EncodeFrame(&lmproto.SendPacket{
				ClientMsgNo: "123",
				ClientSeq:   1,
				ChannelID:   "test",
				ChannelType: 1,
				Payload:     []byte("hello"),
			}, lmproto.LatestVersion)
			assert.NoError(b, err)
			s.OnPacket(c, data)

			result := <-c.WriteChan()
			frame, _, err := s.opts.Proto.DecodeFrame(result, lmproto.LatestVersion)
			assert.NoError(b, err)
			assert.Equal(b, frame.(*lmproto.SendackPacket).ReasonCode, lmproto.ReasonSuccess)
		}
	}
}
