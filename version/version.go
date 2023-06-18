package version

import "embed"

var Version string    // version
var Commit string     // git commit id
var CommitDate string // git commit date
var TreeState string  // git tree state

var StaticFs embed.FS // 静态资源
