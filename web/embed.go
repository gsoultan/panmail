//go:build builtui

package web

import "embed"

//go:embed all:dist
var Dist embed.FS

const IsBuiltUI = true
