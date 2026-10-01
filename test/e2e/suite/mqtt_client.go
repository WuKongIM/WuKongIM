//go:build e2e

package suite

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/eclipse/paho.golang/paho"
)

// MQTTClient is an independent Eclipse Paho MQTT 5 client with bounded inbound
// observation. Product tests must not decode messages through the server codec.
type MQTTClient struct {
	Client   *paho.Client
	Connack  *paho.Connack
	conn     net.Conn
	messages chan *paho.Publish
	errors   chan error
}

// MQTTConnectOptions controls independent wire-client behavior, never server state.
type MQTTConnectOptions struct {
	// ManualAcknowledgements leaves incoming QoS 1 publications pending until Client.Ack.
	ManualAcknowledgements bool
	// ReceiveMaximum advertises an explicit receive window; zero uses the client default.
	ReceiveMaximum uint16
	Will           *paho.WillMessage
	WillProperties *paho.WillProperties
}

// ConnectMQTT authenticates using the wire contract's WEB device token and
// preserves the caller's Clean Start/expiry choices. It makes no retry decisions.
func ConnectMQTT(ctx context.Context, addr, uid, token, clientID string, clean bool, expiry uint32, options ...MQTTConnectOptions) (*MQTTClient, error) {
	if len(options) > 1 {
		return nil, errors.New("MQTT fixture accepts one option set")
	}
	var o MQTTConnectOptions
	if len(options) == 1 {
		o = options[0]
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &MQTTClient{conn: conn, messages: make(chan *paho.Publish, 64), errors: make(chan error, 1)}
	c.Client = paho.NewClient(paho.ClientConfig{
		ClientID: clientID, Conn: conn, PacketTimeout: 5 * time.Second,
		EnableManualAcknowledgment: o.ManualAcknowledgements, SendAcksInterval: time.Millisecond,
		OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(received paho.PublishReceived) (bool, error) {
			select {
			case c.messages <- received.Packet:
				return true, nil
			default:
				err := errors.New("MQTT test receive queue overflow")
				select {
				case c.errors <- err:
				default:
				}
				_ = conn.Close()
				return false, err
			}
		}},
		OnClientError: func(err error) {
			select {
			case c.errors <- err:
			default:
			}
		},
	})
	var receiveMaximum *uint16
	if o.ReceiveMaximum != 0 {
		value := o.ReceiveMaximum
		receiveMaximum = &value
	}
	ack, err := c.Client.Connect(ctx, &paho.Connect{
		ClientID: clientID, Username: uid, UsernameFlag: true, Password: []byte(token), PasswordFlag: true,
		CleanStart: clean, KeepAlive: 30, WillMessage: o.Will, WillProperties: o.WillProperties,
		Properties: &paho.ConnectProperties{SessionExpiryInterval: &expiry, ReceiveMaximum: receiveMaximum, RequestProblemInfo: true, User: paho.UserProperties{{Key: "wk.device_flag", Value: "1"}}},
	})
	if err != nil {
		_ = conn.Close()
		if ack != nil {
			// Preserve the server decision; paho's error text omits it.
			return nil, fmt.Errorf("%w (CONNACK reason 0x%02x)", err, ack.ReasonCode)
		}
		return nil, err
	}
	c.Connack = ack
	return c, nil
}

// Receive returns one publication or a bounded protocol/connection failure.
func (c *MQTTClient) Receive(ctx context.Context) (*paho.Publish, error) {
	select {
	case p := <-c.messages:
		return p, nil
	case err := <-c.errors:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close releases the fixture connection after a normal MQTT DISCONNECT.
func (c *MQTTClient) Close() error {
	if c == nil {
		return nil
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
	err := c.Client.Disconnect(&paho.Disconnect{ReasonCode: 0})
	_ = c.conn.Close()
	return err
}

// Abort closes TCP without DISCONNECT, preserving the server's abnormal-close path.
func (c *MQTTClient) Abort() error { return c.conn.Close() }

// AbortAndWait closes TCP without DISCONNECT and joins Paho's owned workers.
// A canceled wait does not establish that client shutdown has completed.
func (c *MQTTClient) AbortAndWait(ctx context.Context) error {
	if c == nil {
		return nil
	}
	err := c.Abort()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	select {
	case <-c.Client.Done():
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}
