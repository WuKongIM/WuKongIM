package config

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestJSONConfigurationParsersOwnWhitespaceAndRejectMalformedInput(t *testing.T) {
	seeds, err := parseClusterSeeds(`[" 127.0.0.1:7001 ","node-2:7001"]`)
	if err != nil || len(seeds) != 2 || seeds[0] != "127.0.0.1:7001" || seeds[1] != "node-2:7001" {
		t.Fatalf("parseClusterSeeds() = (%v, %v)", seeds, err)
	}
	for _, raw := range []string{`not-json`, `[]`, `[" "]`} {
		if _, err := parseClusterSeeds(raw); err == nil {
			t.Fatalf("parseClusterSeeds(%q) error = nil", raw)
		}
	}

	values, err := parseStringList("WK_TEST_LIST", `[" first ","second"]`)
	if err != nil || len(values) != 2 || values[0] != "first" || values[1] != "second" {
		t.Fatalf("parseStringList() = (%v, %v)", values, err)
	}
	if _, err := parseStringList("WK_TEST_LIST", `{}`); err == nil || !strings.Contains(err.Error(), "WK_TEST_LIST") {
		t.Fatalf("malformed parseStringList() error = %v", err)
	}

	matches, err := parseDiagnosticsDebugMatches(`[{"uid":"u1","sample_rate":0.75,"ttl_seconds":30}]`)
	if err != nil || len(matches) != 1 || matches[0].UID != "u1" || matches[0].SampleRate != 0.75 || matches[0].TTLSeconds != 30 {
		t.Fatalf("parseDiagnosticsDebugMatches() = (%+v, %v)", matches, err)
	}
	for _, raw := range []string{
		`not-json`,
		`[{"sample_rate":1.01}]`,
		`[{"sample_rate":0.5,"ttl_seconds":-1}]`,
	} {
		if _, err := parseDiagnosticsDebugMatches(raw); err == nil {
			t.Fatalf("parseDiagnosticsDebugMatches(%q) error = nil", raw)
		}
	}

	users, err := parseManagerUsers(`[{"username":"admin","password":"secret","permissions":[{"resource":"*","actions":["*"]}]}]`)
	if err != nil || len(users) != 1 || users[0].Username != "admin" || len(users[0].Permissions) != 1 {
		t.Fatalf("parseManagerUsers() = (%+v, %v)", users, err)
	}
	if _, err := parseManagerUsers(`{"username":"admin"}`); err == nil {
		t.Fatal("parseManagerUsers() accepted a non-list payload")
	}

	listeners, err := parseListeners(`[{"name":"tcp","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`)
	if err != nil || len(listeners) != 1 || listeners[0].Name != "tcp" {
		t.Fatalf("parseListeners() = (%+v, %v)", listeners, err)
	}
	if _, err := parseListeners(`[`); err == nil {
		t.Fatal("parseListeners() accepted malformed JSON")
	}
	nodes, err := parseClusterNodes(`[{"id":1,"addr":"127.0.0.1:7001"}]`)
	if err != nil || len(nodes) != 1 || nodes[0].ID != 1 || nodes[0].Addr != "127.0.0.1:7001" {
		t.Fatalf("parseClusterNodes() = (%+v, %v)", nodes, err)
	}
	if _, err := parseClusterNodes(`null trailing`); err == nil {
		t.Fatal("parseClusterNodes() accepted malformed JSON")
	}
}

func TestScalarConfigurationParsersRejectOverflowNonFiniteAndWrongTypes(t *testing.T) {
	if got, err := parseUint64("U64", "42"); err != nil || got != 42 {
		t.Fatalf("parseUint64() = (%d, %v)", got, err)
	}
	if _, err := parseUint64("U64", "-1"); err == nil {
		t.Fatal("parseUint64() accepted negative input")
	}
	if got, err := parseUint32("U32", "42"); err != nil || got != 42 {
		t.Fatalf("parseUint32() = (%d, %v)", got, err)
	}
	if _, err := parseUint32("U32", "4294967296"); err == nil {
		t.Fatal("parseUint32() accepted overflow")
	}
	if got, err := parseUint16("U16", "42"); err != nil || got != 42 {
		t.Fatalf("parseUint16() = (%d, %v)", got, err)
	}
	if _, err := parseUint16("U16", "65536"); err == nil {
		t.Fatal("parseUint16() accepted overflow")
	}
	if got, err := parseBool("BOOL", "true"); err != nil || !got {
		t.Fatalf("parseBool() = (%v, %v)", got, err)
	}
	if _, err := parseBool("BOOL", "yes"); err == nil {
		t.Fatal("parseBool() accepted a non-Go boolean")
	}
	if got, err := parseInt("INT", "-7"); err != nil || got != -7 {
		t.Fatalf("parseInt() = (%d, %v)", got, err)
	}
	if _, err := parseInt("INT", "1.5"); err == nil {
		t.Fatal("parseInt() accepted a fractional value")
	}
	if got, err := parseInt64("INT64", "-9"); err != nil || got != -9 {
		t.Fatalf("parseInt64() = (%d, %v)", got, err)
	}
	if _, err := parseInt64("INT64", "9223372036854775808"); err == nil {
		t.Fatal("parseInt64() accepted overflow")
	}
	if got, err := parseFloat("FLOAT", "0.125"); err != nil || got != 0.125 {
		t.Fatalf("parseFloat() = (%v, %v)", got, err)
	}
	for _, raw := range []string{"not-a-number", "NaN", "+Inf", "-Inf"} {
		if _, err := parseFloat("FLOAT", raw); err == nil {
			t.Fatalf("parseFloat(%q) error = nil", raw)
		}
	}
	if got, err := parseDuration("DURATION", "1500ms"); err != nil || got != 1500*time.Millisecond {
		t.Fatalf("parseDuration() = (%v, %v)", got, err)
	}
	if _, err := parseDuration("DURATION", "soon"); err == nil {
		t.Fatal("parseDuration() accepted invalid duration")
	}
	for _, value := range []float64{0, 0.5, 1} {
		if !validSampleRate(value) {
			t.Fatalf("validSampleRate(%v) = false", value)
		}
	}
	for _, value := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		if validSampleRate(value) {
			t.Fatalf("validSampleRate(%v) = true", value)
		}
	}
}

func TestRequiredConfigurationValueReportsTheExactMissingKey(t *testing.T) {
	values := map[string]string{"WK_PRESENT": "  value  ", "WK_EMPTY": " \t"}
	if got, err := requiredConfigValue(values, "WK_PRESENT"); err != nil || got != "value" {
		t.Fatalf("requiredConfigValue(present) = (%q, %v)", got, err)
	}
	for _, key := range []string{"WK_EMPTY", "WK_MISSING"} {
		if _, err := requiredConfigValue(values, key); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("requiredConfigValue(%q) error = %v", key, err)
		}
	}
}
