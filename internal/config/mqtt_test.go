package config

import (
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/stretchr/testify/require"
)

func TestMQTTConfigTOMLEnvironmentAndDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wukongim.toml")
	base := "[node]\nid=1\ndata_dir='./data'\n[cluster]\nlisten_addr='127.0.0.1:7001'\n"
	writeFile(t, p, base)
	cfg, err := Load(Options{Args: []string{"-config", p}, Environ: cleanEnv()})
	require.NoError(t, err)
	cfg, err = app.NormalizeConfig(cfg)
	require.NoError(t, err)
	require.False(t, cfg.MQTT.Enabled)
	require.EqualValues(t, 86400, cfg.MQTT.SessionExpiryLimitSec)
	require.EqualValues(t, 10000, cfg.MQTT.QuotaMessages)
	require.EqualValues(t, 64<<20, cfg.MQTT.QuotaBytes)
	require.EqualValues(t, 64, cfg.MQTT.WindowLimit)
	writeFile(t, p, base+"[mqtt]\nenable=true\nlisten_addr='127.0.0.1:1883'\nnamespace='device'\nmax_connections=100\nmax_subscriptions=12\nworkers=3\nmax_packet_bytes=65536\nsession_expiry_limit_sec=7200\nquota_messages=42\nquota_bytes=1048576\nwindow_limit=8\n")
	cfg, err = Load(Options{Args: []string{"-config", p}, Environ: append(cleanEnv(), "WK_MQTT_LISTEN_ADDR=127.0.0.1:1884", "WK_MQTT_NAMESPACE=overridden")})
	require.NoError(t, err)
	require.True(t, cfg.MQTT.Enabled)
	require.Equal(t, "127.0.0.1:1884", cfg.MQTT.ListenAddr)
	require.Equal(t, "overridden", cfg.MQTT.Namespace)
	require.Equal(t, 100, cfg.MQTT.MaxConnections)
	require.Equal(t, 12, cfg.MQTT.MaxSubscriptions)
	require.Equal(t, 3, cfg.MQTT.Workers)
	require.EqualValues(t, 65536, cfg.MQTT.MaxPacketBytes)
	require.EqualValues(t, 7200, cfg.MQTT.SessionExpiryLimitSec)
	require.EqualValues(t, 42, cfg.MQTT.QuotaMessages)
	require.EqualValues(t, 1048576, cfg.MQTT.QuotaBytes)
	require.EqualValues(t, 8, cfg.MQTT.WindowLimit)
}
