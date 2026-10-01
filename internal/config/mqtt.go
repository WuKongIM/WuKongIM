package config

import "github.com/WuKongIM/WuKongIM/internal/app"

func loadMQTTConfig(values map[string]string) (app.MQTTConfig, error) {
	var c app.MQTTConfig
	var err error
	if raw := configValue(values, "WK_MQTT_ENABLE"); raw != "" {
		c.Enabled, err = parseBool("WK_MQTT_ENABLE", raw)
		if err != nil {
			return c, err
		}
	}
	c.ListenAddr = configValue(values, "WK_MQTT_LISTEN_ADDR")
	c.Namespace = configValue(values, "WK_MQTT_NAMESPACE")
	if raw := configValue(values, "WK_MQTT_MAX_CONNECTIONS"); raw != "" {
		c.MaxConnections, err = parseInt("WK_MQTT_MAX_CONNECTIONS", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_MAX_SUBSCRIPTIONS"); raw != "" {
		c.MaxSubscriptions, err = parseInt("WK_MQTT_MAX_SUBSCRIPTIONS", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_WORKERS"); raw != "" {
		c.Workers, err = parseInt("WK_MQTT_WORKERS", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_MAX_PACKET_BYTES"); raw != "" {
		c.MaxPacketBytes, err = parseUint32("WK_MQTT_MAX_PACKET_BYTES", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_SESSION_EXPIRY_LIMIT_SEC"); raw != "" {
		c.SessionExpiryLimitSec, err = parseUint32("WK_MQTT_SESSION_EXPIRY_LIMIT_SEC", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_QUOTA_MESSAGES"); raw != "" {
		c.QuotaMessages, err = parseUint64("WK_MQTT_QUOTA_MESSAGES", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_QUOTA_BYTES"); raw != "" {
		c.QuotaBytes, err = parseUint64("WK_MQTT_QUOTA_BYTES", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_WINDOW_LIMIT"); raw != "" {
		c.WindowLimit, err = parseUint16("WK_MQTT_WINDOW_LIMIT", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_STORAGE_NODE_BYTES"); raw != "" {
		c.StorageNodeBytes, err = parseUint64("WK_MQTT_STORAGE_NODE_BYTES", raw)
		if err != nil {
			return c, err
		}
	}
	if raw := configValue(values, "WK_MQTT_STORAGE_CLUSTER_BYTES"); raw != "" {
		c.StorageClusterBytes, err = parseUint64("WK_MQTT_STORAGE_CLUSTER_BYTES", raw)
		if err != nil {
			return c, err
		}
	}
	return app.NormalizeMQTTConfig(c)
}
