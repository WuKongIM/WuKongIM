//go:build e2e

package suite

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/eclipse/paho.golang/packets"
	"github.com/gorilla/websocket"
)

// MQTTWebSocket observes independent MQTT packets as a byte stream across binary
// WebSocket messages. It owns no retries, authentication or product state.
type MQTTWebSocket struct {
	Conn   *websocket.Conn
	reader io.Reader
}

// DialMQTTWebSocket preserves the exact offered subprotocol list. The small write
// buffer lets WriteFragmented exercise real WebSocket continuation frames.
func DialMQTTWebSocket(ctx context.Context, url string, protocols ...string) (*MQTTWebSocket, *http.Response, error) {
	dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 5 * time.Second, WriteBufferSize: 32}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, response, err
	}
	conn.SetReadLimit(1 << 20)
	return &MQTTWebSocket{Conn: conn}, response, nil
}

// EncodeMQTTPackets uses Eclipse Paho's codec, never the server's MQTT codec.
func EncodeMQTTPackets(values ...packets.Packet) ([]byte, error) {
	var body bytes.Buffer
	for _, value := range values {
		if _, err := value.WriteTo(&body); err != nil {
			return nil, err
		}
	}
	return body.Bytes(), nil
}

// WritePackets coalesces one or more MQTT packets into one binary WebSocket message.
func (c *MQTTWebSocket) WritePackets(ctx context.Context, values ...packets.Packet) error {
	body, err := EncodeMQTTPackets(values...)
	if err != nil {
		return err
	}
	if err = c.Conn.SetWriteDeadline(mqttWebSocketDeadline(ctx)); err != nil {
		return err
	}
	return c.Conn.WriteMessage(websocket.BinaryMessage, body)
}

// WriteFragmented emits a single binary message through a 32-byte writer buffer,
// in small chunks that force continuation frames for packets larger than 32 bytes.
func (c *MQTTWebSocket) WriteFragmented(ctx context.Context, value packets.Packet) error {
	body, err := EncodeMQTTPackets(value)
	if err != nil {
		return err
	}
	if err = c.Conn.SetWriteDeadline(mqttWebSocketDeadline(ctx)); err != nil {
		return err
	}
	writer, err := c.Conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	for len(body) > 0 {
		n := min(16, len(body))
		if _, err = writer.Write(body[:n]); err != nil {
			_ = writer.Close()
			return err
		}
		body = body[n:]
	}
	return writer.Close()
}

// ReadPacket enforces binary server messages and retains MQTT packets split
// across message boundaries. The caller must serialize reads and writes.
func (c *MQTTWebSocket) ReadPacket(ctx context.Context) (*packets.ControlPacket, error) {
	// Delivery discovery may take up to the bounded ten-second quiet hint.
	deadline := time.Now().Add(20 * time.Second)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if err := c.Conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	return packets.ReadPacket(c)
}
func (c *MQTTWebSocket) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	for {
		if c.reader == nil {
			kind, r, err := c.Conn.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				return 0, errors.New("MQTT server sent nonbinary WebSocket data")
			}
			c.reader = r
		}
		n, err := c.reader.Read(out)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
func mqttWebSocketDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(5 * time.Second)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	return deadline
}

// MQTTUserProperty reads the first named property without dumping message bodies.
func MQTTUserProperty(properties *packets.Properties, name string) string {
	if properties != nil {
		for _, property := range properties.User {
			if property.Key == name {
				return property.Value
			}
		}
	}
	return ""
}
