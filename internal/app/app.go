// Package app wires the domain layer to the Wails runtime and exposes the
// methods bound to the frontend.
package app

import (
	"context"
	"embed"
	"sync"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

// App holds the state shared by all methods bound to the frontend.
type App struct {
	// tokens is where the Slack user token lives.
	tokens config.TokenStore
	// checker verifies a token against Slack.
	checker config.TokenChecker

	mu  sync.Mutex
	ctx context.Context
	// token is the outcome of the last token check.
	token TokenStatus
}

// Option customises an App; production uses the defaults, tests replace
// the keychain and the Slack call.
type Option func(*App)

// WithTokenStore replaces the token storage.
func WithTokenStore(s config.TokenStore) Option {
	return func(a *App) { a.tokens = s }
}

// WithTokenChecker replaces the auth.test call.
func WithTokenChecker(c config.TokenChecker) Option {
	return func(a *App) { a.checker = c }
}

// New creates an App.
func New(opts ...Option) *App {
	a := &App{
		tokens:  config.NewTokenStore(),
		checker: config.SlackChecker(),
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// startup stores the Wails context so runtime methods can be called later
// and verifies the stored token, so the window opens already knowing
// whether it can talk to Slack.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()

	a.checkToken(ctx)
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
