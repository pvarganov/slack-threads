// Package frontend embeds the built web assets served by the Wails asset server.
package frontend

import "embed"

// Assets holds the built frontend bundle served by the Wails asset server.
//
//go:embed all:dist
var Assets embed.FS
