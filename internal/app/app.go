// Package app wires the domain layer to the Wails runtime and exposes the
// methods bound to the frontend.
package app

import (
	"context"
	"embed"
	"sync"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// EventProgress carries sync.Progress events to the frontend.
const EventProgress = "sync:progress"

// Emitter publishes an event to the frontend. Production uses the Wails
// runtime; tests collect the events instead.
type Emitter func(ctx context.Context, event string, data ...interface{})

// App holds the state shared by all methods bound to the frontend.
type App struct {
	// tokens is where the Slack user token lives.
	tokens config.TokenStore
	// checker verifies a token against Slack.
	checker config.TokenChecker
	// cfg is the loaded configuration; the build function needs it.
	cfg config.Config
	// store is the local copy of the threads; nil when it could not be
	// opened, which every binding reports as "not ready".
	store Storage
	// sync is the orchestration; nil until a working token wires it up.
	sync Syncer
	// build assembles the sync service around a verified token.
	build buildFunc
	// emit publishes progress events to the frontend.
	emit Emitter
	// locks keeps two syncs of the same thread apart.
	locks *keyLock

	mu  sync.Mutex
	ctx context.Context
	// token is the outcome of the last token check.
	token TokenStatus
	// pumping is true once the progress forwarder is running, so
	// re-wiring after a token change does not start a second one.
	pumping bool
	// rewired signals pumpProgress that a.sync was just replaced, so it
	// stops waiting on the old service's Progress channel and rereads it.
	rewired chan struct{}
}

// Option customises an App; production uses the defaults, tests replace
// the keychain, the Slack call, the storage and the sync service.
type Option func(*App)

// WithTokenStore replaces the token storage.
func WithTokenStore(s config.TokenStore) Option {
	return func(a *App) { a.tokens = s }
}

// WithTokenChecker replaces the auth.test call.
func WithTokenChecker(c config.TokenChecker) Option {
	return func(a *App) { a.checker = c }
}

// WithConfig sets the configuration the sync service is built from.
func WithConfig(cfg config.Config) Option {
	return func(a *App) { a.cfg = cfg }
}

// WithStorage sets the open store.
func WithStorage(s Storage) Option {
	return func(a *App) { a.store = s }
}

// WithSyncer injects a ready sync service, which also disables the
// token-driven wiring: what is passed in is what the bindings use.
func WithSyncer(s Syncer) Option {
	return func(a *App) {
		a.sync = s
		a.build = nil
	}
}

// WithEmitter replaces the frontend event sink.
func WithEmitter(e Emitter) Option {
	return func(a *App) { a.emit = e }
}

// New creates an App.
func New(opts ...Option) *App {
	a := &App{
		tokens:  config.NewTokenStore(),
		checker: config.SlackChecker(),
		build:   buildService,
		emit:    wailsruntime.EventsEmit,
		locks:   newKeyLock(),
		rewired: make(chan struct{}, 1),
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// startup stores the Wails context so runtime methods can be called later
// and verifies the stored token, so the window opens already knowing
// whether it can talk to Slack. A token that works also wires up the sync
// service: without one there is nothing to sync with.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()

	a.checkToken(ctx)
}

// wire builds the sync service around a verified token and starts
// forwarding its progress events. It is a no-op when the service was
// injected or the store never opened.
func (a *App) wire(ctx context.Context, token string) {
	a.mu.Lock()
	build, st := a.build, a.store
	a.mu.Unlock()

	if build == nil || st == nil || token == "" {
		return
	}

	svc := build(a.cfg, st, token)
	if svc == nil {
		return
	}

	a.mu.Lock()
	previous := a.sync
	a.sync = svc
	start := !a.pumping
	a.pumping = true
	a.mu.Unlock()

	if start {
		go a.pumpProgress(ctx)
	} else {
		// pumpProgress may be parked reading the old service's Progress
		// channel; wake it so it picks up the one just installed.
		select {
		case a.rewired <- struct{}{}:
		default:
		}
	}

	if previous != nil {
		// Best-effort: the new service is already in place, so a claude
		// process that failed to shut down is not worth failing over.
		_ = previous.Close()
	}
}

// pumpProgress forwards sync progress to the frontend until the app
// shuts down. Re-wiring after a token change replaces the service, so the
// channel is read through the current one on every iteration; rewired
// breaks it out of a wait on a service that was just replaced.
func (a *App) pumpProgress(ctx context.Context) {
	for {
		a.mu.Lock()
		svc := a.sync
		a.mu.Unlock()

		if svc == nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-a.rewired:
			continue
		case p, ok := <-svc.Progress():
			if !ok {
				return
			}

			a.emit(ctx, EventProgress, p)
		}
	}
}

// Version reports the application version to the frontend.
func (a *App) Version() string {
	return Version
}

// Version is the application version, overridden at build time via -ldflags.
var Version = "dev"

// Run starts the Wails application with the given embedded frontend assets.
func Run(assets embed.FS) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()

	db, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck // closing on shutdown, nothing to report to

	a := New(WithConfig(cfg), WithStorage(db))

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
