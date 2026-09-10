package app

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/permalink"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
	"github.com/pavelvarganov/slack-threads/internal/translate"
)

// ErrBusy is returned when the same thread is already being synced. It is
// not a failure of the operation: the user simply pressed the button
// twice, and the first press is still working.
var ErrBusy = errors.New("app: thread is already being refreshed")

// UserError is what every bound method returns instead of a domain error:
// Wails hands the frontend only the error text, so that text has to be the
// explanation shown to the user.
type UserError struct {
	// Message is the Russian explanation displayed in the UI.
	Message string
	// Fixable is true when the user can do something about it right away
	// (enter another token, join the channel, retry later).
	Fixable bool
	// Err is the underlying cause, kept for errors.Is and for logs.
	Err error
}

func (e *UserError) Error() string { return e.Message }

// Unwrap exposes the cause, so callers (and tests) can still match the
// domain sentinels behind the message.
func (e *UserError) Unwrap() error { return e.Err }

// userError converts a domain error into the message the frontend shows.
// It returns nil for a nil error, so it can wrap a call's result directly.
//
//nolint:cyclop // a flat translation table reads better than nested helpers
func userError(err error) error {
	if err == nil {
		return nil
	}

	var already *UserError
	if errors.As(err, &already) {
		return already
	}

	msg, fixable := describe(err)

	return &UserError{Message: msg, Fixable: fixable, Err: err}
}

// describe picks the user-facing text and whether the user can act on it.
//
//nolint:cyclop,funlen,gocyclo // one case per domain error, deliberately flat
func describe(err error) (string, bool) {
	var (
		problem *config.TokenProblem
		turn    *translate.TurnError
		httpErr *slackapi.HTTPError
		netErr  net.Error
	)

	switch {
	case errors.As(err, &problem):
		return problem.Message, problem.Fixable

	case errors.Is(err, permalink.ErrInvalid):
		return "Это не похоже на ссылку на сообщение Slack. Скопируйте ссылку через «Copy link» в меню сообщения.", true

	case errors.Is(err, store.ErrThreadExists):
		return "Этот тред уже добавлен.", false

	case errors.Is(err, store.ErrNotFound):
		return "Тред не найден: возможно, он уже удалён из приложения.", false

	case errors.Is(err, ErrBusy):
		return "Этот тред сейчас обновляется. Дождитесь окончания.", false

	case errors.Is(err, syncsvc.ErrEmptyReply), errors.Is(err, translate.ErrEmptyReply):
		return "Текст ответа пустой.", true

	case errors.Is(err, slackapi.ErrInvalidAuth):
		return "Slack отклонил токен: он истёк или отозван. Сохраните новый user-токен.", true

	case errors.Is(err, slackapi.ErrNotInChannel):
		return "Вы не состоите в этом канале — Slack не отдаёт тред. Вступите в канал и повторите.", true

	case errors.Is(err, slackapi.ErrChannelNotFound):
		return "Канал недоступен: его нет или у токена нет к нему доступа.", true

	case errors.Is(err, slackapi.ErrThreadNotFound), errors.Is(err, syncsvc.ErrEmptyThread):
		return "Тред недоступен: он удалён в Slack или скрыт от вашего токена.", false

	case errors.Is(err, slackapi.ErrPostRestricted):
		return "Slack не разрешает писать в этот канал: во внешние каналы Slack Connect отправка недоступна.", false

	case errors.Is(err, slackapi.ErrRateLimited):
		return "Slack ограничил частоту запросов. Повторите через минуту.", true

	case errors.As(err, &httpErr):
		return fmt.Sprintf("Slack ответил ошибкой (HTTP %d). Повторите позже.", httpErr.StatusCode), true

	case errors.Is(err, translate.ErrTimeout):
		return "Переводчик не ответил вовремя. Повторите обновление.", true

	case errors.Is(err, translate.ErrSessionClosed):
		return "Сессия переводчика закрылась. Повторите обновление — она запустится заново.", true

	case isProcessError(err):
		return "Не удалось запустить claude. Проверьте, что бинарник установлен и доступен в PATH.", true

	case errors.As(err, &turn):
		return "Перевод не удался: " + turn.Message, true

	case isBadAnswer(err):
		return "Переводчик вернул неожиданный ответ. Повторите обновление.", true

	case errors.Is(err, context.Canceled):
		return "Операция прервана.", false

	case errors.Is(err, context.DeadlineExceeded):
		return "Операция не уложилась в отведённое время. Повторите позже.", true

	case errors.As(err, &netErr):
		return "Нет связи со Slack. Проверьте сеть и повторите.", true
	}

	return "Не удалось выполнить операцию: " + err.Error(), false
}

// isProcessError reports a claude process that died or would not start.
func isProcessError(err error) bool {
	var pe *translate.ProcessError

	return errors.As(err, &pe)
}

// isBadAnswer reports an answer from claude that could not be used: either
// not the JSON that was asked for, or missing translations.
func isBadAnswer(err error) bool {
	var (
		protocol *translate.ProtocolError
		response *translate.ResponseError
	)

	return errors.As(err, &protocol) || errors.As(err, &response)
}
