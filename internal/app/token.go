package app

import (
	"context"
	"errors"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/config"
)

// tokenCheckTimeout bounds the auth.test call made on startup, so a
// hanging network cannot keep the window empty.
const tokenCheckTimeout = 15 * time.Second

// TokenStatus is what the frontend shows about the Slack token: either who
// the app is logged in as, or a message explaining what to do about it.
type TokenStatus struct {
	// OK is true when a stored token passed auth.test.
	OK bool `json:"ok"`
	// User is the handle of the token owner, when OK.
	User string `json:"user"`
	// Team is the workspace name, when OK.
	Team string `json:"team"`
	// Message explains the problem, in Russian, when not OK.
	Message string `json:"message"`
	// Fixable is true when saving another token would resolve it, which
	// is the UI's cue to offer the token dialog.
	Fixable bool `json:"fixable"`
}

// checkToken verifies the stored token and caches the outcome.
func (a *App) checkToken(ctx context.Context) TokenStatus {
	// The timeout bounds the auth.test call only: the wiring below has to
	// outlive it, its context lives as long as the window does.
	checkCtx, cancel := context.WithTimeout(ctx, tokenCheckTimeout)
	defer cancel()

	token, info, err := config.ResolveToken(checkCtx, a.tokens, a.checker)

	status := TokenStatus{OK: true, User: info.User, Team: info.Team}
	if err != nil {
		status = TokenStatus{Message: problemMessage(err), Fixable: isFixable(err)}
	}

	a.mu.Lock()
	a.token = status
	a.mu.Unlock()

	// Only a token Slack accepted is worth building a Slack client and a
	// translator around.
	if status.OK {
		a.wire(ctx, token)
	}

	return status
}

// CheckToken re-runs the startup check and returns the fresh status.
func (a *App) CheckToken() TokenStatus {
	return a.checkToken(a.context())
}

// TokenStatus returns the cached result of the last check, which is the
// one the window was opened with.
func (a *App) TokenStatus() TokenStatus {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.token
}

// SaveToken stores a token and immediately reports whether it works, so
// the dialog can stay open on a bad token.
func (a *App) SaveToken(token string) TokenStatus {
	if err := config.ValidateFormat(token); err != nil {
		return TokenStatus{
			Message: "Это не похоже на пользовательский токен Slack: он должен начинаться с xoxp-.",
			Fixable: true,
		}
	}

	if err := a.tokens.SetToken(token); err != nil {
		return TokenStatus{Message: saveMessage(err), Fixable: false}
	}

	return a.checkToken(a.context())
}

// context returns the Wails context once startup has run, and a plain
// background context before that (and in tests).
func (a *App) context() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.ctx != nil {
		return a.ctx
	}

	return context.Background()
}

// problemMessage picks the user-facing text out of an error, falling back
// to a generic line for anything that is not a *config.TokenProblem.
func problemMessage(err error) string {
	var problem *config.TokenProblem
	if errors.As(err, &problem) {
		return problem.Message
	}

	return "Не удалось проверить токен Slack: " + err.Error()
}

// isFixable reports whether the UI should offer to enter another token.
func isFixable(err error) bool {
	var problem *config.TokenProblem
	if errors.As(err, &problem) {
		return problem.Fixable
	}

	return false
}

// saveMessage explains why a token could not be written.
func saveMessage(err error) string {
	if errors.Is(err, config.ErrReadOnlyStore) {
		return "Токен задан переменной окружения SLACK_THREADS_TOKEN — уберите её, чтобы хранить токен в связке ключей."
	}

	return "Не удалось сохранить токен в связке ключей: " + err.Error()
}
