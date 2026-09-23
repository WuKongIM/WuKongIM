package mqtt_test

import (
	"errors"
	"strings"
	"testing"

	access "github.com/WuKongIM/WuKongIM/internal/access/mqtt"
	wire "github.com/WuKongIM/WuKongIM/pkg/protocol/mqtt"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

func TestWillMappingReservesDurableIdentityBeforeAcceptance(t *testing.T) {
	key := "mqtt-will-v1:" + strings.Repeat("a", 64)
	w := &wire.Will{Topic: "wk/v1/users/Ym9i/messages", QoS: 1, Properties: []wire.Property{
		{ID: wire.UserProperty, Text: "wk.client_msg_no", Value: "client"},
		{ID: wire.CorrelationData},
	}}
	base, err := access.MapWill(w, "n", "c")
	if err != nil {
		t.Fatal(err)
	}
	w.Properties[1].Data = make([]byte, publication.MaxEncodedBytes-len(base.Metadata)-2-len(key))
	input, err := access.MapWill(w, "n", "c")
	if err != nil {
		t.Fatal(err)
	}
	m, err := publication.Decode(input.Metadata)
	if err != nil || input.Metadata[0] != 1 || m.ServerWillKey != "" {
		t.Fatalf("template already contains server identity: %+v %v", m, err)
	}
	m.ServerWillKey = key
	published, err := publication.Encode(m)
	if err != nil || len(published) != publication.MaxEncodedBytes {
		t.Fatalf("accepted template cannot publish: %d %v", len(published), err)
	}
	w.Properties[1].Data = append(w.Properties[1].Data, 0)
	_, err = access.MapWill(w, "n", "c")
	var protocolErr *wire.Error
	if !errors.As(err, &protocolErr) || protocolErr.Reason != wire.PacketTooLarge {
		t.Fatalf("accepted unpublishable template: %v", err)
	}
	// Ordinary publishes need no server identity reservation.
	p := &wire.Publish{Topic: w.Topic, QoS: 1, PacketID: 1, Properties: w.Properties}
	if _, err := access.MapPublish(p, "n", "c", 1000); err != nil {
		t.Fatalf("ordinary limit changed: %v", err)
	}
}
