package app

import (
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

func TestGatewayAuthenticatorUsesStoredDeviceTokenWhenEnabled(t *testing.T) {
	cluster := &fakeManagerCluster{
		nodeID: 1,
		devices: map[fakeManagerDeviceKey]metadb.Device{
			{uid: "u1", deviceFlag: int64(frame.APP)}: {
				UID:         "u1",
				DeviceFlag:  int64(frame.APP),
				Token:       "token-1",
				DeviceLevel: int64(frame.DeviceLevelMaster),
			},
		},
	}
	app := &App{
		cfg:     Config{Gateway: GatewayConfig{TokenAuthOn: true}},
		cluster: cluster,
		logger:  wklog.NewNop(),
	}
	app.wireUsers()
	authenticator := app.newGatewayAuthenticator(1)
	ctx := &gateway.Context{Protocol: "jsonrpc"}

	accepted, err := authenticator.Authenticate(ctx, &frame.ConnectPacket{
		UID:        "u1",
		DeviceFlag: frame.APP,
		Token:      "token-1",
	})
	if err != nil {
		t.Fatalf("Authenticate(valid) error = %v", err)
	}
	if accepted.Connack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("Authenticate(valid) reason = %v, want success", accepted.Connack.ReasonCode)
	}
	if level := accepted.SessionValues[gateway.SessionValueDeviceLevel]; level != frame.DeviceLevelMaster {
		t.Fatalf("Authenticate(valid) device level = %v, want master", level)
	}

	for _, token := range []string{"", "wrong-token"} {
		rejected, err := authenticator.Authenticate(ctx, &frame.ConnectPacket{
			UID:        "u1",
			DeviceFlag: frame.APP,
			Token:      token,
		})
		if err != nil {
			t.Fatalf("Authenticate(%q) error = %v", token, err)
		}
		if rejected.Connack.ReasonCode != frame.ReasonAuthFail {
			t.Fatalf("Authenticate(%q) reason = %v, want auth fail", token, rejected.Connack.ReasonCode)
		}
	}
}

func TestGatewayAuthenticatorAllowsMissingTokenWhenDisabled(t *testing.T) {
	app := &App{cfg: Config{Gateway: GatewayConfig{TokenAuthOn: false}}}
	authenticator := app.newGatewayAuthenticator(1)

	result, err := authenticator.Authenticate(&gateway.Context{Protocol: "jsonrpc"}, &frame.ConnectPacket{
		UID:        "u1",
		DeviceFlag: frame.APP,
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if result.Connack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("Authenticate() reason = %v, want success", result.Connack.ReasonCode)
	}
}
