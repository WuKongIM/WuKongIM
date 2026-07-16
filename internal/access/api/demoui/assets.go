package demoui

import "embed"

// embeddedDist contains the production Demo bundle compiled by Vite.
//
//go:embed all:dist
var embeddedDist embed.FS
