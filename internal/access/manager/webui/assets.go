package webui

import "embed"

// embeddedDist contains the immutable manager web production bundle.
//
//go:embed all:dist
var embeddedDist embed.FS
