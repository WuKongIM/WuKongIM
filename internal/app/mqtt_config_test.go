package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMQTTConfigRejectsInvalidLimitsBeforeConstruction(t *testing.T) {
	for _, mutate := range []func(*MQTTConfig){
		func(c *MQTTConfig) { c.ListenAddr = "bad" },
		func(c *MQTTConfig) { c.Namespace = "\x00" },
		func(c *MQTTConfig) { c.MaxConnections = -1 },
		func(c *MQTTConfig) { c.MaxSubscriptions = 1025 },
		func(c *MQTTConfig) { c.Workers = 129 },
		func(c *MQTTConfig) { c.MaxPacketBytes = (1 << 20) + 1 },
		func(c *MQTTConfig) { c.WindowLimit = 1025 },
		func(c *MQTTConfig) { c.SessionExpiryLimitSec = 86401 },
	} {
		c := MQTTConfig{Enabled: true, ListenAddr: "127.0.0.1:1883"}
		mutate(&c)
		_, err := NormalizeConfig(Config{MQTT: c})
		require.ErrorIs(t, err, ErrInvalidConfig)
	}
}
