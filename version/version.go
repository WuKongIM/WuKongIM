package version

import "embed"

var Version string    // version
var Commit string     // git commit id
var CommitDate string // git commit date
var TreeState string  // git tree state

var WebFs embed.FS  // monitor静态资源
var DemoFs embed.FS // demo静态资源
