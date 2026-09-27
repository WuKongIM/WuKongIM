package app

import (
	"fmt"
	"net"
	"strconv"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
)

// MQTTConfig bounds the optional MQTT 5 entry and its durable Session work.
// Zero values select defaults; Enabled remains false unless explicitly enabled.
type MQTTConfig struct {
	// Enabled opens the MQTT listener after cluster and dependent workers are ready.
	Enabled bool
	// ListenAddr is the TCP bind address, default 0.0.0.0:1883; TLS terminates upstream.
	ListenAddr string
	// Namespace is the stable broker ClientID namespace shared by all cluster nodes.
	Namespace string
	// MaxConnections includes pending/closing owners, default 100,000, maximum 1,000,000.
	MaxConnections int
	// MaxSubscriptions counts pending, active and removing intents, default 128, maximum 1024.
	MaxSubscriptions int
	// Workers bounds each connection/delivery cohort, default 16, maximum 128.
	Workers int
	// MaxPacketBytes bounds an inbound packet, default and maximum 1 MiB.
	MaxPacketBytes uint32
	// SessionExpiryLimitSec caps requested offline lifetime, default 24 hours.
	SessionExpiryLimitSec uint32
	// QuotaMessages and QuotaBytes bound logical per-Session backlog; first exceeded wins.
	// Defaults are 10,000 messages and 64 MiB, pending scale qualification.
	QuotaMessages uint64
	QuotaBytes    uint64
	// WindowLimit bounds durable QoS 1 exchanges, default 64, maximum 1024.
	WindowLimit uint16
}

// NormalizeMQTTConfig is pure and shared by startup and effective configuration views.
func NormalizeMQTTConfig(c MQTTConfig) (MQTTConfig, error) {
	if c.ListenAddr == "" {
		c.ListenAddr = "0.0.0.0:1883"
	}
	if c.Namespace == "" {
		c.Namespace = "main"
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = 100000
	}
	if c.MaxSubscriptions == 0 {
		c.MaxSubscriptions = 128
	}
	if c.Workers == 0 {
		c.Workers = 16
	}
	if c.MaxPacketBytes == 0 {
		c.MaxPacketBytes = 1 << 20
	}
	if c.SessionExpiryLimitSec == 0 {
		c.SessionExpiryLimitSec = 86400
	}
	if c.QuotaMessages == 0 {
		c.QuotaMessages = 10000
	}
	if c.QuotaBytes == 0 {
		c.QuotaBytes = 64 << 20
	}
	if c.WindowLimit == 0 {
		c.WindowLimit = 64
	}
	_, port, err := net.SplitHostPort(c.ListenAddr)
	n, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || portErr != nil || n == 0 || !contract.ValidIdentity(c.Namespace, 1024) || c.MaxConnections < 1 || c.MaxConnections > 1000000 || c.MaxSubscriptions < 1 || c.MaxSubscriptions > 1024 || c.Workers < 1 || c.Workers > 128 || c.MaxPacketBytes > 1<<20 || c.WindowLimit > 1024 || c.SessionExpiryLimitSec > 86400 {
		return MQTTConfig{}, fmt.Errorf("%w: invalid MQTT address, namespace or limits", ErrInvalidConfig)
	}
	return c, nil
}
