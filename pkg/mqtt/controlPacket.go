package mqtt

import "io"

// controlPacket MQTT control packet codec interface
type ControlPacket interface {
	Encode(w io.Writer) error
	Decode(r io.Reader, remainingLen uint32) error
}
