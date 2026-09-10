package app

import (
	"context"
	"net/http"
	"strconv"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
	"github.com/pavelvarganov/slack-threads/internal/translate"
)

// buildFunc assembles the sync service around a verified Slack token.
// It is a field on App so tests can wire fakes instead of a real claude
// process and a real Slack endpoint.
type buildFunc func(cfg config.Config, st Storage, token string) Syncer

// buildService is the production wiring: an HTTP Slack client, a claude
// session manager and the sync orchestration on top of the open store.
func buildService(cfg config.Config, st Storage, token string) Syncer {
	opts := []slackapi.Option{
		slackapi.WithHTTPClient(&http.Client{Timeout: cfg.SlackTimeout}),
	}

	// The profile cache is the store itself, when the store is the real
	// one: it spares a users.info call per author on every refresh.
	if db, ok := st.(*store.Store); ok {
		opts = append(opts, slackapi.WithUserCache(store.NewUserCache(db)))
	}

	slack := slackapi.New(token, opts...)

	manager := translate.NewManager(translate.ExecRunner{Binary: cfg.ClaudeBinary}, translate.Config{
		Model:           cfg.Model,
		SystemPrompt:    translate.SystemPrompt,
		ResponseTimeout: cfg.ResponseTimeout,
		IdleTimeout:     cfg.IdleTimeout,
	})

	sessions := translate.ManagerSessions{Manager: manager, Resume: resumeFunc(st)}
	translator := translate.NewTranslator(sessions)

	syncStore, ok := st.(syncsvc.Store)
	if !ok {
		return nil
	}

	return syncsvc.New(syncStore, slack, translator, syncsvc.WithModel(cfg.Model))
}

// resumeFunc looks up the claude session stored on a thread, so a
// restarted app continues the same conversation instead of losing the
// thread's translation context.
func resumeFunc(st Storage) func(threadID string) string {
	return func(threadID string) string {
		id, err := strconv.ParseInt(threadID, 10, 64)
		if err != nil {
			return ""
		}

		thread, err := st.GetThread(context.Background(), id)
		if err != nil {
			return ""
		}

		return thread.ClaudeSessionID
	}
}
