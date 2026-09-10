# Slack Threads: десктопное приложение для чтения тредов с переводом на русский

## Overview

Десктопное приложение (Go + Wails) для работы с отдельными Slack-тредами на русском языке.

Проблема: рабочие треды в англоязычных каналах длинные и технические. Читать их через
Slack медленно, а разовый перевод в чате не сохраняется и не обновляется.

Что делает приложение:
- тред добавляется по permalink и сохраняется локально навсегда (SQLite)
- при добавлении тред целиком переводится на русский через Claude CLI с сохранением
  контекста всего обсуждения (одна долгоживущая сессия на тред)
- кнопка «Обновить» на треде и «Обновить все» перечитывают треды и переводят только
  изменившееся; автоматического поллинга нет
- для каждого треда генерируется блок «Суть» (о чём тред, что ждут от пользователя)
- ответ пишется по-русски, переводится на английский, показывается обратный перевод
  для контроля, отправляется в тред через `chat.postMessage`
- тред можно удалить (со всеми сообщениями и переводами) или архивировать
  (перестать обновлять, перевод сохранить)

Ключевые решения, принятые при планировании:
- **Slack-доступ**: собственный Slack app с user-токеном (`xoxp-`), токен хранится в
  системном keychain. Создание приложения и получение апрува в workspace — ручной шаг
  пользователя (см. Post-Completion)
- **Переводчик**: бинарник `claude` как подпроцесс, долгоживущая сессия на тред через
  `stream-json` по stdin/stdout. Качество Opus, оплата по подписке, API-ключ не нужен
- **Обновление**: полное перечитывание треда с пагинацией и сверка по `ts` + хешу
  текста — ловит и новые сообщения, и правки старых
- **UI**: Wails (Go-бэкенд + webview-фронт), потому что контент — форматированный текст
  с код-блоками, ссылками и списками

## Context (from discovery)

- Проект создаётся с нуля в `~/repo/slack-threads` — существующей кодовой базы нет
- Окружение: Go 1.26.0 (darwin/arm64), `ralphex` установлен, `claude` CLI 2.1.267
- Wails не установлен — ставится через `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- Локального Slack-токена в системе нет, `slck` не установлен. MCP-коннектор claude.ai
  приложению недоступен — нужен собственный токен
- Правила перевода уже сформулированы в скилле `~/.claude/skills/slack-thread/`
  (`SKILL.md` разделы 3-4 и `glossary.md`) — их нужно перенести в системный промпт
  переводчика, а не изобретать заново

## Development Approach

- **Testing approach**: TDD — каждая задача начинается с падающего теста
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

Дополнительно для этого проекта:
- внешние зависимости (Slack HTTP API, бинарник `claude`) закрываются интерфейсами и
  подменяются в тестах — тесты не ходят в сеть и не запускают `claude`
- SQLite через `modernc.org/sqlite` (чистый Go, без cgo) — кросс-компиляция и простая сборка

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach above)
- Slack-клиент тестируется против `httptest.Server` с записанными формами ответов
- Переводчик тестируется против фейкового исполняемого файла (тестовый скрипт,
  печатающий заранее заданные stream-json события), интерфейс `Runner` подменяется
- Store тестируется на реальной SQLite-базе во временной директории
- **E2E tests**: UI-фреймворка для e2e в проекте нет; проверка UI — ручная, сценарии
  перечислены в Post-Completion

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): код, тесты, документация в этом репозитории
- **Post-Completion** (no checkboxes): создание Slack app, апрув в workspace, ручная
  проверка UI, сборка `.app`

## Implementation Steps

### Task 1: Каркас проекта и проверка контрактов внешних инструментов

Спайк: до написания кода зафиксировать реальные интерфейсы CLI и Slack API, чтобы не
строить обвязку на предположениях.

- [x] выполнить `claude --help` и записать в `docs/claude-cli-contract.md` фактические
      флаги: `-p`, `--input-format stream-json`, `--output-format stream-json`,
      `--verbose`, `--append-system-prompt`, `--model`, `--resume`, флаги ограничения
      инструментов и режима разрешений
- [x] запустить `claude` вручную в режиме stream-json, отправить одно сообщение и
      записать в тот же документ реальные формы событий stdin/stdout (`user`,
      `assistant`, `result`, поле `session_id`)
- [x] `go mod init github.com/pavelvarganov/slack-threads`, установить Wails CLI,
      сгенерировать каркас `wails init` с vanilla-фронтендом
- [x] создать структуру каталогов: `cmd/slack-threads`, `internal/permalink`,
      `internal/store`, `internal/slackapi`, `internal/translate`, `internal/sync`,
      `internal/app`, `frontend`
- [x] добавить `Makefile` (цели `test`, `lint`, `build`, `dev`) и `.golangci.yml`
- [x] добавить smoke-тест, проверяющий что пакеты собираются (`go build ./...` в CI-цели)
- [x] run tests - must pass before next task

➕ Уточнения по факту реализации:
- для изоляции переводчика нужны **оба** флага `--tools "" --strict-mcp-config`:
  без второго MCP-серверы пользователя подключаются даже при пустом списке инструментов
- Wails собирает корень модуля, поэтому точка входа продублирована: `main.go` в корне
  (нужен `wails build`) и `cmd/slack-threads/main.go` — оба трёхстрочные обёртки над
  `internal/app.Run`; встраивание ассетов вынесено в пакет `frontend`
- добавлен `internal/config` (в списке каталогов его не было, но он есть в Technical Details)
- smoke-тест живёт в `internal/buildsmoke` и прогоняет `go build ./...` и `go vet ./...`

### Task 2: Разбор Slack permalink

- [ ] написать тесты для `permalink.Parse` (табличные): ссылка на корневое сообщение,
      ссылка с `thread_ts` в query, ссылка на канал `app.slack.com/client/...`,
      приватная группа `G…`, личка `D…`, мусор и пустая строка
- [ ] написать тест на конвертацию `p1788872615903009` → `1788872615.903009`
- [ ] реализовать `internal/permalink`: возврат `channelID`, `threadTS`, `focusTS`,
      `workspace` и типизированные ошибки для нераспознанной ссылки
- [ ] написать тесты на обратную сборку permalink по `channelID` + `ts` (нужно для
      ссылки на отправленное сообщение)
- [ ] run tests - must pass before next task

### Task 3: Хранилище — схема, миграции, треды

- [ ] написать тесты для `store.Open` на временной базе: создание схемы, идемпотентность
      повторного открытия, применение миграций по версии
- [ ] реализовать `internal/store` на `modernc.org/sqlite` с версионированными миграциями
- [ ] описать схему: `threads` (id, channel_id, thread_ts, workspace, title, added_at,
      last_fetched_at, archived, claude_session_id), `messages` (id, thread_id, ts,
      user_id, text, raw_json, edited_ts, text_hash), `translations` (message_id, text_ru,
      model, created_at), `summaries` (thread_id, text_ru, based_on_ts, updated_at),
      `users` (id, display_name, real_name, is_bot, updated_at), `drafts` (thread_id,
      text_ru, text_en, back_ru, updated_at)
- [ ] написать тесты на CRUD тредов: добавление, список, получение, архивирование,
      удаление с каскадом (сообщения, переводы, суть, черновик уходят вместе)
- [ ] написать тест на уникальность треда по паре (channel_id, thread_ts)
- [ ] реализовать методы работы с тредами
- [ ] run tests - must pass before next task

### Task 4: Хранилище — сообщения, переводы, пользователи

- [ ] написать тесты на upsert сообщений: новое сообщение вставляется, неизменённое не
      трогается, изменённый текст обновляет запись и инвалидирует перевод
- [ ] написать тесты на вычисление `text_hash` и на выборку «сообщения без актуального
      перевода»
- [ ] реализовать upsert сообщений с инвалидацией переводов по хешу
- [ ] написать тесты на сохранение и чтение переводов, сути и кэша пользователей
- [ ] реализовать соответствующие методы
- [ ] run tests - must pass before next task

### Task 5: Slack-клиент — чтение треда

- [ ] написать тесты на `conversations.replies` против `httptest.Server`: одна страница,
      пагинация по `response_metadata.next_cursor`, пустой тред, `ok:false` с
      `error: channel_not_found`
- [ ] написать тесты на обработку `429`: уважение `Retry-After`, ограниченное число
      повторов, возврат типизированной ошибки при исчерпании
- [ ] реализовать `internal/slackapi` с интерфейсом `Client`, транспортом на
      `net/http`, user-токеном в заголовке `Authorization: Bearer`
- [ ] реализовать `FetchThread(ctx, channelID, threadTS)` с полной пагинацией
- [ ] написать тесты на маппинг сырого ответа в доменную структуру сообщения
      (ts, user, bot_id, text, edited.ts, reactions, subtype)
- [ ] run tests - must pass before next task

### Task 6: Slack-клиент — пользователи и рендер mrkdwn

- [ ] написать тесты на `users.info` с кэшированием в store: повторный запрос того же
      id не ходит в сеть, неизвестный пользователь не роняет загрузку треда
- [ ] реализовать разрешение имён пользователей и ботов с кэшем
- [ ] написать тесты для конвертера Slack mrkdwn: `<http://url|label>`, `<@U123>`,
      `<#C123|name>`, HTML-энтити `&amp; &lt; &gt;`, тройные бэктики, инлайн-код,
      вложенные и многострочные случаи
- [ ] реализовать конвертер mrkdwn → структурированный вид для фронтенда
- [ ] run tests - must pass before next task

### Task 7: Переводчик — управление сессией claude

- [ ] написать тесты на `translate.Session` с подменённым `Runner`: запуск процесса,
      отправка сообщения, чтение ответа до события `result`, извлечение `session_id`
- [ ] написать тесты на ошибки: процесс завершился с ненулевым кодом, невалидный JSON в
      stdout, таймаут ответа, отмена по context
- [ ] реализовать `internal/translate` с интерфейсом `Runner` (реальная реализация —
      `os/exec` с `claude`) и менеджером сессий: одна сессия на тред, ленивый запуск,
      закрытие по неактивности, переиспользование `session_id` через `--resume`
- [ ] написать тесты на то, что сессия переиспользуется для того же треда и не
      переиспользуется для другого
- [ ] реализовать ограничение инструментов и permission-mode для процесса-переводчика
      (переводчику не нужны файловые и сетевые инструменты)
- [ ] run tests - must pass before next task

### Task 8: Переводчик — перевод сообщений треда

- [ ] перенести правила перевода и глоссарий из `~/.claude/skills/slack-thread/` в
      `internal/translate/prompt.go` (системный промпт: не переводить код, бэктики,
      идентификаторы, URL, ключи задач, @упоминания, имена сервисов; сохранять списки и
      переносы; русские сообщения оставлять как есть)
- [ ] написать тесты на сборку промпта: батч сообщений превращается в запрос с
      идентификаторами, ранее переведённые сообщения передаются как контекст
- [ ] написать тесты на разбор структурированного ответа: соответствие переводов
      идентификаторам, отсутствующий перевод, лишний идентификатор, невалидный JSON
- [ ] реализовать `TranslateMessages(ctx, threadID, msgs) ([]Translation, error)` с
      разбиением на батчи по объёму текста
- [ ] написать тесты на батчинг: длинный тред режется на несколько запросов, порядок
      сохраняется
- [ ] run tests - must pass before next task

### Task 9: Переводчик — генерация «Сути» треда

- [ ] написать тесты на промпт для сути: на вход идут переводы всех сообщений, на выход —
      короткий текст (о чём тред и что ждут от пользователя)
- [ ] написать тесты на пропуск генерации для треда из одного-двух коротких сообщений
- [ ] реализовать `Summarize(ctx, threadID, translated) (string, error)`
- [ ] написать тесты на то, что суть пересобирается только при появлении новых или
      изменённых сообщений (сверка `based_on_ts`)
- [ ] run tests - must pass before next task

### Task 10: Сервис синхронизации треда

- [ ] написать тесты на `sync.AddThread`: парсинг ссылки, загрузка треда, сохранение,
      перевод всех сообщений, генерация сути, повторное добавление того же треда не
      создаёт дубль
- [ ] написать тесты на `sync.RefreshThread`: новые сообщения переводятся, неизменённые
      не перепереводятся, изменённое сообщение перепереводится, удалённое в Slack
      помечается в базе
- [ ] написать тесты на `sync.RefreshAll`: обход неархивированных тредов, ошибка в одном
      треде не прерывает остальные, результат содержит per-thread статусы
- [ ] реализовать `internal/sync` поверх store, slackapi и translate
- [ ] написать тесты на прогресс-события (загрузка → перевод N из M → суть) для UI
- [ ] реализовать канал прогресс-событий
- [ ] run tests - must pass before next task

### Task 11: Ответы — перевод RU→EN и отправка

- [ ] написать тесты на `translate.DraftReply`: русский текст → английский + обратный
      перевод на русский, сохранение кода, идентификаторов, @упоминаний и эмодзи
- [ ] реализовать `DraftReply(ctx, threadID, ru) (en, backRU string, err error)`
- [ ] написать тесты на сохранение и чтение черновика в store
- [ ] написать тесты на `chat.postMessage` против `httptest.Server`: успешная отправка в
      тред с `thread_ts`, ошибка внешнего Slack Connect канала, `not_in_channel`,
      `ratelimited`
- [ ] реализовать отправку и возврат permalink отправленного сообщения
- [ ] написать тесты на то, что после успешной отправки черновик очищается, а тред
      помечается требующим обновления
- [ ] run tests - must pass before next task

### Task 12: Конфигурация и хранение токена

- [ ] написать тесты на загрузку конфигурации: путь к базе, путь к бинарнику `claude`,
      модель, таймауты, значения по умолчанию
- [ ] написать тесты на работу с токеном через подменяемый интерфейс `TokenStore`:
      сохранение, чтение, отсутствие токена
- [ ] реализовать `internal/config` и хранение токена в системном keychain
      (`github.com/zalando/go-keyring`), с фолбэком на переменную окружения
- [ ] написать тесты на валидацию токена (формат `xoxp-`, проверка через `auth.test`)
- [ ] реализовать проверку токена при старте с понятной ошибкой в UI
- [ ] run tests - must pass before next task

### Task 13: Wails-биндинги

- [ ] написать тесты на методы `internal/app`: `AddThread(url)`, `ListThreads()`,
      `GetThread(id)`, `RefreshThread(id)`, `RefreshAll()`, `DeleteThread(id)`,
      `ArchiveThread(id)`, `DraftReply(id, ru)`, `SendReply(id, en)`, `SaveToken(t)`
- [ ] написать тесты на то, что ошибки доменного слоя превращаются в понятные
      пользователю сообщения, а не в сырые тексты ошибок
- [ ] реализовать биндинги и проброс прогресс-событий во фронтенд через Wails events
- [ ] написать тесты на защиту от параллельного обновления одного треда
- [ ] реализовать блокировку по треду
- [ ] run tests - must pass before next task

### Task 14: Интерфейс

- [ ] сверстать двухколоночный макет: слева список тредов с кнопками «Добавить по
      ссылке» и «Обновить все», справа лента треда
- [ ] реализовать ленту: «Суть» вверху, сообщения с именем и временем, перевод на
      русском, тумблер «показать оригинал», код-блоки моноширинным с горизонтальным
      скроллом, реакции как есть
- [ ] реализовать панель ответа: русский текст → «Перевести» → английский и обратный
      перевод → «Отправить», с блокировкой отправки до перевода
- [ ] реализовать индикаторы: прогресс загрузки и перевода, состояние «нет токена»,
      ошибки Slack и переводчика
- [ ] реализовать удаление и архивирование треда с подтверждением
- [ ] проверить, что окно и лента корректно ведут себя в тёмной и светлой теме macOS
- [ ] run tests - must pass before next task

### Task 15: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] verify edge cases are handled: тред без прав доступа, удалённый тред, тред только
      с ботами, очень длинный тред с пагинацией, недоступный бинарник `claude`,
      протухший токен, обрыв сети посреди обновления
- [ ] run full test suite (unit tests)
- [ ] run linter - all issues must be fixed
- [ ] verify test coverage meets project standard (80%+)

### Task 16: [Final] Update documentation

- [ ] написать `README.md`: назначение, установка, создание Slack app и список scopes,
      первый запуск, сборка `.app`
- [ ] зафиксировать в `docs/` контракт `claude` CLI (из Task 1) и схему базы
- [ ] описать известные ограничения: отсутствие автополлинга, лимиты Slack на
      `conversations.replies`, невозможность отправки в Slack Connect каналы

## Technical Details

**Структура проекта**

```
~/repo/slack-threads/
  cmd/slack-threads/main.go     точка входа, сборка зависимостей, запуск Wails
  internal/permalink/           разбор и сборка ссылок Slack
  internal/store/               SQLite: схема, миграции, репозитории
  internal/slackapi/            HTTP-клиент Slack: replies, users.info, postMessage
  internal/translate/           сессии claude, промпты, перевод, суть, черновики ответов
  internal/sync/                оркестрация: добавить, обновить один, обновить все
  internal/config/              конфиг и токен (keychain)
  internal/app/                 Wails-биндинги
  frontend/                     HTML/CSS/JS
  docs/plans/                   планы
```

**Поток «добавить тред»**

permalink → `permalink.Parse` → `slackapi.FetchThread` (с пагинацией) →
`store.UpsertMessages` → `slackapi.ResolveUsers` → `translate.TranslateMessages`
(сессия треда, батчи) → `store.SaveTranslations` → `translate.Summarize` →
`store.SaveSummary`. Прогресс транслируется в UI на каждом шаге.

**Поток «обновить»**

Тред перечитывается целиком. Для каждого сообщения считается `text_hash`; новые и
изменившиеся попадают в очередь перевода, остальные не трогаются. Перевод дельты идёт в
ту же сессию треда, поэтому термины согласованы с уже переведённой частью. Суть
пересобирается только если дельта непустая.

**Протокол общения с claude**

Долгоживущий процесс на тред:
`claude -p --input-format stream-json --output-format stream-json --verbose
--append-system-prompt <правила перевода> --model opus`.
Запросы пишутся в stdin JSON-строками, ответы читаются из stdout до события `result`.
`session_id` из первого ответа сохраняется в `threads.claude_session_id` и используется
для `--resume` после перезапуска приложения. Точные формы событий фиксируются в Task 1 и
не додумываются по памяти.

**Формат запроса на перевод**

На вход модели идёт JSON-массив `{id, author, text}`, на выход ожидается
`{id, text_ru}` для каждого элемента. Соответствие проверяется по id; недостающие
переводы вызывают повторный запрос только для пропущенных сообщений.

**Ограничения Slack**

`conversations.replies` для приложений не из Marketplace ограничен примерно одним
запросом в минуту и небольшим числом сообщений в ответе. Пагинация длинного треда при
первом импорте поэтому медленная: клиент должен уважать `Retry-After`, показывать
прогресс и не считать `429` фатальной ошибкой. Точное поведение для user-токена
проверяется эмпирически на живом токене (см. Post-Completion).

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Внешние шаги перед первым запуском**

- Создать Slack app на api.slack.com/apps в workspace overgearcom
- Запросить User Token Scopes: `channels:history`, `groups:history`, `im:history`,
  `mpim:history`, `users:read`, `chat:write`
- Установить приложение в workspace; если самостоятельная установка запрещена —
  получить апрув администратора workspace
- Сохранить полученный `xoxp-` токен в приложении (он ляжет в системный keychain)

**Ручная проверка**

- Добавить реальный длинный тред с код-блоками и проверить качество перевода и
  сохранность кода, идентификаторов и ссылок
- Дописать сообщение в тред из Slack и проверить, что «Обновить» подтягивает и
  переводит только его
- Отредактировать старое сообщение в Slack и проверить, что перевод обновился
- Отправить ответ из приложения и убедиться, что он попал в тред, а не в канал
- Проверить поведение на внешнем (Slack Connect) канале: отправка должна давать понятную
  ошибку с возможностью скопировать текст
- Проверить фактические лимиты `conversations.replies` для user-токена и при
  необходимости скорректировать стратегию пагинации

**Сборка и распространение**

- `wails build` для получения `.app`
- Подпись и нотаризация не планируются: приложение личное, запускается локально
