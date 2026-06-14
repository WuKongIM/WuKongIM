package bench

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	channelPickRoundRobin = "round_robin"
	channelPickRandom     = "random"
	defaultChannel        = "wkcli-bench-channel"
	defaultChannelPrefix  = "wkcli-bench-channel"
	defaultClientMsgNo    = "wkcli-bench"
	defaultUIDPrefix      = "wkcli-bench-u"
	defaultDevicePrefix   = "wkcli-bench-device"
)

type sendConfig struct {
	ContextName  string
	ContextDir   string
	ServerAddrs  []string
	GatewayAddrs []string
	BenchToken   string

	Clients      int
	Messages     int
	Size         string
	Payload      string
	PayloadBytes int
	BatchSize    int
	Throughput   int
	Sleep        time.Duration
	AckTimeout   time.Duration
	ConnectRate  int

	Channel       string
	Channels      int
	ChannelPrefix string
	ChannelType   string
	ChannelTypeID uint8
	ChannelPick   string
	RandomSeed    int64

	UIDPrefix         string
	DevicePrefix      string
	Token             string
	ClientMsgNoPrefix string
	RunID             string

	NoProgress     bool
	JSON           bool
	CSVPath        string
	ProgressWriter io.Writer
}

func normalizeSendConfig(cfg sendConfig) (sendConfig, error) {
	if cfg.Clients <= 0 {
		return sendConfig{}, fmt.Errorf("--clients must be greater than zero")
	}
	if cfg.Messages <= 0 {
		return sendConfig{}, fmt.Errorf("--msgs must be greater than zero")
	}
	if cfg.Channels <= 0 {
		return sendConfig{}, fmt.Errorf("--channels must be greater than zero")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1
	}
	if cfg.AckTimeout <= 0 {
		cfg.AckTimeout = 5 * time.Second
	}
	if strings.TrimSpace(cfg.Channel) == "" {
		cfg.Channel = defaultChannel
	}
	if strings.TrimSpace(cfg.ChannelPrefix) == "" {
		cfg.ChannelPrefix = defaultChannelPrefix
	}
	if strings.TrimSpace(cfg.ChannelType) == "" {
		cfg.ChannelType = "group"
	}
	channelTypeID, normalizedChannelType, err := parseChannelType(cfg.ChannelType)
	if err != nil {
		return sendConfig{}, err
	}
	cfg.ChannelType = normalizedChannelType
	cfg.ChannelTypeID = channelTypeID
	if strings.TrimSpace(cfg.ChannelPick) == "" {
		cfg.ChannelPick = channelPickRoundRobin
	}
	if cfg.ChannelPick != channelPickRoundRobin && cfg.ChannelPick != channelPickRandom {
		return sendConfig{}, fmt.Errorf("--channel-pick must be %q or %q", channelPickRoundRobin, channelPickRandom)
	}
	if strings.TrimSpace(cfg.UIDPrefix) == "" {
		cfg.UIDPrefix = defaultUIDPrefix
	}
	if strings.TrimSpace(cfg.DevicePrefix) == "" {
		cfg.DevicePrefix = defaultDevicePrefix
	}
	if strings.TrimSpace(cfg.ClientMsgNoPrefix) == "" {
		cfg.ClientMsgNoPrefix = defaultClientMsgNo
	}
	if strings.TrimSpace(cfg.RunID) == "" {
		runID, err := newRunID()
		if err != nil {
			return sendConfig{}, err
		}
		cfg.RunID = runID
	}
	if cfg.Payload != "" {
		cfg.PayloadBytes = len([]byte(cfg.Payload))
	} else {
		payloadBytes, err := parseByteSize(cfg.Size)
		if err != nil {
			return sendConfig{}, err
		}
		cfg.PayloadBytes = payloadBytes
	}
	if cfg.PayloadBytes <= 0 {
		return sendConfig{}, fmt.Errorf("--size must be greater than zero")
	}
	return cfg, nil
}

func parseChannelType(value string) (uint8, string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "person", "1":
		return frame.ChannelTypePerson, "person", nil
	case "group", "2":
		return frame.ChannelTypeGroup, "group", nil
	case "cmd", "command":
		return frame.ChannelTypeGroup, "cmd", nil
	default:
		return 0, "", fmt.Errorf("--channel-type must be person, group, or cmd")
	}
}

func parseByteSize(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = "128B"
	}
	upper := strings.ToUpper(trimmed)
	multipliers := []struct {
		suffix string
		value  int64
	}{
		{"KIB", 1024},
		{"MIB", 1024 * 1024},
		{"GIB", 1024 * 1024 * 1024},
		{"KB", 1000},
		{"MB", 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"B", 1},
	}
	multiplier := int64(1)
	number := upper
	for _, item := range multipliers {
		if strings.HasSuffix(upper, item.suffix) {
			multiplier = item.value
			number = strings.TrimSpace(strings.TrimSuffix(upper, item.suffix))
			break
		}
	}
	if number == "" {
		return 0, fmt.Errorf("invalid --size %q", value)
	}
	n, err := strconv.ParseFloat(number, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid --size %q", value)
	}
	size := int64(n * float64(multiplier))
	if size <= 0 || size > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("invalid --size %q", value)
	}
	return int(size), nil
}

func commandChannelID(channelID string) string {
	return runtimechannelid.ToCommandChannel(channelID)
}

// newRunID returns a compact random token that scopes idempotency keys to one bench run.
func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate bench run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
