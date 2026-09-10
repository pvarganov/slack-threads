package config

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

// TokenPrefix is what a Slack user token starts with. Bot tokens
// (`xoxb-`) cannot read a thread as the user, so they are rejected early
// instead of failing later on a scope error.
const TokenPrefix = "xoxp-"

// ErrTokenFormat means the string is not shaped like a user token.
var ErrTokenFormat = errors.New("config: not a slack user token")

// ValidateFormat checks the token locally, before any network call.
func ValidateFormat(token string) error {
	token = strings.TrimSpace(token)

	switch {
	case token == "":
		return fmt.Errorf("%w: token is empty", ErrTokenFormat)
	case !strings.HasPrefix(token, TokenPrefix):
		return fmt.Errorf("%w: expected the %s… prefix", ErrTokenFormat, TokenPrefix)
	case len(token) <= len(TokenPrefix):
		return fmt.Errorf("%w: nothing after the %s prefix", ErrTokenFormat, TokenPrefix)
	}

	return nil
}

// TokenChecker verifies a token against Slack. It is a function so the
// startup check can be tested without a network.
type TokenChecker func(ctx context.Context, token string) (slackapi.AuthInfo, error)

// SlackChecker returns a TokenChecker calling auth.test with a throwaway
// client. The options are passed through so tests can point it at an
// httptest server.
func SlackChecker(opts ...slackapi.Option) TokenChecker {
	return func(ctx context.Context, token string) (slackapi.AuthInfo, error) {
		return slackapi.New(token, opts...).AuthTest(ctx)
	}
}

// TokenProblem is a token that cannot be used, with a message meant for
// the user rather than for a log. The UI shows Message verbatim and uses
// Fixable to decide whether to open the token dialog.
type TokenProblem struct {
	// Message is the explanation shown in the UI, in Russian.
	Message string
	// Fixable is true when entering another token would help.
	Fixable bool
	// Err is the underlying cause, kept for logs and errors.Is.
	Err error
}

func (e *TokenProblem) Error() string { return e.Message }

// Unwrap exposes the cause, so errors.Is(err, ErrNoToken) and friends work.
func (e *TokenProblem) Unwrap() error { return e.Err }

// CheckToken validates a token: format first, then auth.test. It returns
// the identity behind the token, or a *TokenProblem explaining what the
// user has to do.
func CheckToken(ctx context.Context, token string, check TokenChecker) (slackapi.AuthInfo, error) {
	if err := ValidateFormat(token); err != nil {
		return slackapi.AuthInfo{}, &TokenProblem{
			Message: "Это не похоже на пользовательский токен Slack: он должен начинаться с xoxp-.",
			Fixable: true,
			Err:     err,
		}
	}

	if check == nil {
		return slackapi.AuthInfo{}, errors.New("config: no token checker configured")
	}

	info, err := check(ctx, strings.TrimSpace(token))
	if err != nil {
		return slackapi.AuthInfo{}, &TokenProblem{Message: authMessage(err), Fixable: fixable(err), Err: err}
	}

	return info, nil
}

// ResolveToken is the startup check: read the stored token and verify it.
// A missing token is reported as a *TokenProblem too, because from the
// UI's point of view "нет токена" and "токен отклонён" lead to the same
// screen.
func ResolveToken(ctx context.Context, store TokenStore, check TokenChecker) (string, slackapi.AuthInfo, error) {
	token, err := store.Token()

	switch {
	case errors.Is(err, ErrNoToken):
		return "", slackapi.AuthInfo{}, &TokenProblem{
			Message: "Токен Slack не задан. Откройте настройки и сохраните user-токен (xoxp-…).",
			Fixable: true,
			Err:     err,
		}
	case err != nil:
		return "", slackapi.AuthInfo{}, &TokenProblem{
			Message: "Не удалось прочитать токен из связки ключей. Разрешите доступ и повторите.",
			Fixable: false,
			Err:     err,
		}
	}

	info, err := CheckToken(ctx, token, check)
	if err != nil {
		return "", slackapi.AuthInfo{}, err
	}

	return token, info, nil
}

// authMessage turns a failed auth.test into an explanation the user can
// act on.
func authMessage(err error) string {
	switch {
	case errors.Is(err, slackapi.ErrInvalidAuth):
		return "Slack отклонил токен: он недействителен или отозван. Сохраните новый токен."
	case errors.Is(err, slackapi.ErrMissingScope):
		return "Токену не хватает прав. Нужны scopes: channels:history, groups:history, " +
			"im:history, mpim:history, users:read, chat:write."
	case errors.Is(err, slackapi.ErrRateLimited):
		return "Slack ограничил частоту запросов. Повторите проверку через минуту."
	}

	return fmt.Sprintf("Не удалось проверить токен: %v", err)
}

// fixable reports whether replacing the token would help; a rate limit or
// a network hiccup would not.
func fixable(err error) bool {
	switch {
	case errors.Is(err, slackapi.ErrInvalidAuth), errors.Is(err, slackapi.ErrMissingScope):
		return true
	case errors.Is(err, slackapi.ErrRateLimited):
		return false
	}

	var apiErr *slackapi.APIError

	return errors.As(err, &apiErr)
}
