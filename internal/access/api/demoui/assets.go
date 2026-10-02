package demoui

import "embed"

// embeddedHomeDist contains the read-only catalog for all five Demo scenarios.
//
//go:embed all:homedist
var embeddedHomeDist embed.FS

// embeddedDist contains the production Demo bundle compiled by Vite.
//
//go:embed all:dist
var embeddedDist embed.FS

// embeddedStreamDist contains the independent EasySDK stream Demo bundle.
//
//go:embed all:streamdist
var embeddedStreamDist embed.FS

// embeddedSupportDist contains the read-only customer support Demo interface.
//
//go:embed all:supportdist
var embeddedSupportDist embed.FS

// embeddedAgentDist contains the read-only tool-calling Agent Demo interface.
//
//go:embed all:agentdist
var embeddedAgentDist embed.FS

// embeddedMQTTDist contains the smart-store UI; MQTT clients run in the browser.
//
//go:embed all:mqttdist
var embeddedMQTTDist embed.FS
