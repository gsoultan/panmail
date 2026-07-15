//go:build !builtui

package web

import "embed"

var Dist embed.FS

const IsBuiltUI = false
