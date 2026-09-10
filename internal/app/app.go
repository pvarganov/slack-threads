// Package app wires the domain layer to the Wails runtime and exposes the
// methods bound to the frontend.
package app

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// App holds the state shared by all methods bound to the frontend.
type App struct {
	ctx context.Context
}

// New creates an App.
func New() *App {
	return &App{}
}

// startup stores the Wails context so runtime methods can be called later.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Version reports the application version to the frontend.
func (a *App) Version() string {
	return Version
}

// Version is the application version, overridden at build time via -ldflags.
var Version = "dev"

// Run starts the Wails application with the given embedded frontend assets.
func Run(assets embed.FS) error {
	a := New()

	return wails.Run(&options.App{
		Title:  "Slack Threads",
		Width:  1280,
		Height: 860,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: a.startup,
		Bind: []interface{}{
			a,
		},
	})
}
